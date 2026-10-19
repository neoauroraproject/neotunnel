#!/usr/bin/env bash
# NeoTunnel installer for Ubuntu / Debian.
#
#   curl -fsSL https://raw.githubusercontent.com/neoauroraproject/neotunnel/main/install.sh | sudo bash
#
# Options (after "bash -s --" when piping):
#   --no-panel        install without the web panel (destination servers)
#   --port N          panel port (default: a random free port)
#   --version vX.Y.Z  install a specific release (default: latest)
#   --binary PATH     offline install from a downloaded binary
#   --no-boost        skip the network boost (BBR, fq, buffers)
#   --no-menu         don't open the menu when done
#
# Re-running it upgrades NeoTunnel and keeps the configuration and password.
set -euo pipefail

REPO="neoauroraproject/neotunnel"
PANEL=1 PORT="" VERSION="latest" BINARY="" BOOST=1 MENU=1

while [ $# -gt 0 ]; do
  case "$1" in
    --no-panel) PANEL=0 ;;
    --port) PORT="$2"; shift ;;
    --version) VERSION="$2"; shift ;;
    --binary) BINARY="$2"; shift ;;
    --no-boost) BOOST=0 ;;
    --no-menu) MENU=0 ;;
    -h|--help) sed -n '2,16p' "$0" 2>/dev/null || true; exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
  shift
done

c_ok=$'\033[38;5;78m' c_dim=$'\033[2m' c_b=$'\033[1m' c_acc=$'\033[38;5;44m' c_err=$'\033[38;5;203m' c_warn=$'\033[38;5;221m' c_0=$'\033[0m'
step() { printf '%s›%s %s\n' "$c_acc" "$c_0" "$*"; }
ok()   { printf '%s✓%s %s\n' "$c_ok" "$c_0" "$*"; }
warn() { printf '%s!%s %s\n' "$c_warn" "$c_0" "$*"; }
die()  { printf '%s✗ %s%s\n' "$c_err" "$*" "$c_0" >&2; exit 1; }

# fetch URL FILE [progress]: downloads, and if a configured proxy is the
# problem (a common cause of "failed to connect to 127.0.0.1"), retries
# without any proxy.
fetch() {
  local opts=(-fL --retry 2 --connect-timeout 15)
  [ "${3:-}" = progress ] && opts+=(--progress-bar) || opts+=(-sS)
  curl "${opts[@]}" "$1" -o "$2" && return 0
  warn "download failed; retrying without proxy settings"
  curl "${opts[@]}" --noproxy '*' "$1" -o "$2"
}

[ "$(id -u)" -eq 0 ] || die "run as root: curl -fsSL ... | sudo bash"
[ "$(uname -s)" = "Linux" ] || die "NeoTunnel runs on Linux"
case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) die "unsupported CPU $(uname -m) (amd64 and arm64 only)" ;;
esac
NAME="neotunnel-linux-$ARCH"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

printf '\n%s◆ NeoTunnel%s %sinstaller%s\n\n' "$c_acc" "$c_0" "$c_dim" "$c_0"

# 1. Binary ---------------------------------------------------------------
if [ -n "$BINARY" ]; then
  step "using local binary $BINARY"
  cp "$BINARY" "$TMP/$NAME"
  [ -f "$(dirname "$BINARY")/SHA256SUMS" ] && grep " $NAME\$" "$(dirname "$BINARY")/SHA256SUMS" > "$TMP/SHA256SUMS" || true
else
  if [ "$VERSION" = "latest" ]; then
    BASE="https://github.com/$REPO/releases/latest/download"
  else
    BASE="https://github.com/$REPO/releases/download/$VERSION"
  fi
  command -v curl >/dev/null || { apt-get update -qq && apt-get install -y -qq curl >/dev/null; }
  step "downloading $NAME ($VERSION)"
  fetch "$BASE/$NAME" "$TMP/$NAME" progress || die "download failed: $BASE/$NAME
  No access to GitHub from this server? Download the file elsewhere, copy it here and run:
  sudo bash install.sh --binary ./$NAME"
  fetch "$BASE/SHA256SUMS" "$TMP/SHA256SUMS.all" || die "could not download the checksums"
  grep " $NAME\$" "$TMP/SHA256SUMS.all" > "$TMP/SHA256SUMS" || die "no checksum for $NAME in the release"
fi
if [ -s "$TMP/SHA256SUMS" ]; then
  (cd "$TMP" && sha256sum -c --quiet SHA256SUMS) || die "checksum mismatch: the download is corrupted or was tampered with"
  ok "checksum verified"
else
  warn "no SHA256SUMS next to the binary; skipping verification"
fi
chmod +x "$TMP/$NAME"
"$TMP/$NAME" version >/dev/null || die "the binary does not run on this system"

# 2. System ---------------------------------------------------------------
step "loading kernel modules"
MODULES="sit ipip ip6_tunnel ip_gre ip6_gre vxlan geneve fou wireguard tun l2tp_eth l2tp_ip l2tp_ip6 xfrm_user esp4 esp6"
for m in $MODULES; do modprobe "$m" 2>/dev/null || true; done
printf '%s\n' $MODULES > /etc/modules-load.d/neotunnel.conf
if ! command -v nft >/dev/null; then
  step "installing nftables"
  (apt-get update -qq && apt-get install -y -qq nftables >/dev/null) || warn "could not install nftables; port forwarding needs it"
fi

UPGRADE=0
[ -x /usr/local/bin/neotunnel ] && UPGRADE=1
install -m 755 "$TMP/$NAME" /usr/local/bin/neotunnel
ok "neotunnel $(neotunnel version) installed"

[ "$BOOST" = 1 ] && { neotunnel boost >/dev/null 2>&1 && ok "network boost applied (BBR, fq, buffers)" || true; }

# 3. Panel ----------------------------------------------------------------
CREDS=""
if [ "$PANEL" = 1 ]; then
  if neotunnel panel info >/dev/null 2>&1 && [ -z "$PORT" ]; then
    ok "panel already set up; keeping its port and password"
  else
    ARGS=()
    [ -n "$PORT" ] && ARGS+=(--port "$PORT")
    CREDS=$(NO_COLOR=1 neotunnel panel setup "${ARGS[@]}") || die "panel setup failed"
  fi
fi

# 4. Service --------------------------------------------------------------
neotunnel install >/dev/null
ok "service running (systemd unit: neotunnel)"
[ "$UPGRADE" = 1 ] && ok "upgrade complete; configuration kept"

echo
if [ "$PANEL" = 1 ]; then
  printf '  %sWeb panel%s\n' "$c_b" "$c_0"
  if [ -n "$CREDS" ]; then
    printf '%s\n' "$CREDS" | sed -n '/address\|username\|password/p'
    echo
    printf '  %sSave the password now: it is shown only once (reset: neotunnel panel password).%s\n' "$c_warn" "$c_0"
  else
    NO_COLOR=1 neotunnel panel info | sed -n '/address\|username/p'
  fi
  printf '  %sThe certificate is self-signed: accept the browser warning once.%s\n\n' "$c_dim" "$c_0"
fi
printf '  Manage everything from the terminal with: %sneotunnel%s\n' "$c_b" "$c_0"
printf '  News and releases: %shttps://t.me/neoaurora%s\n\n' "$c_b" "$c_0"

# 5. Menu -----------------------------------------------------------------
# Open the menu on the user's terminal (stdin is this script when piped).
if [ "$MENU" = 1 ] && [ -t 1 ] && { : </dev/tty; } 2>/dev/null; then
  printf '  %sPress Enter to open the menu (Ctrl+C to skip)…%s ' "$c_acc" "$c_0"
  if read -r _ </dev/tty; then
    exec /usr/local/bin/neotunnel </dev/tty
  fi
fi
