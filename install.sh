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
#   --no-optimize     skip the BBR / buffer tuning
#
# Re-running it upgrades NeoTunnel and keeps the configuration and password.
set -euo pipefail

REPO="neoauroraproject/neotunnel"
PANEL=1 PORT="" VERSION="latest" BINARY="" OPTIMIZE=1

while [ $# -gt 0 ]; do
  case "$1" in
    --no-panel) PANEL=0 ;;
    --port) PORT="$2"; shift ;;
    --version) VERSION="$2"; shift ;;
    --binary) BINARY="$2"; shift ;;
    --no-optimize) OPTIMIZE=0 ;;
    -h|--help) sed -n '2,15p' "$0" 2>/dev/null || true; exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
  shift
done

c_ok=$'\033[38;5;78m' c_dim=$'\033[2m' c_b=$'\033[1m' c_acc=$'\033[38;5;44m' c_err=$'\033[38;5;203m' c_0=$'\033[0m'
step() { printf '%s›%s %s\n' "$c_acc" "$c_0" "$*"; }
ok()   { printf '%s✓%s %s\n' "$c_ok" "$c_0" "$*"; }
die()  { printf '%s✗ %s%s\n' "$c_err" "$*" "$c_0" >&2; exit 1; }

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

printf '\n%s  NeoTunnel%s %sinstaller%s\n\n' "$c_b" "$c_0" "$c_dim" "$c_0"

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
  curl -fL --retry 3 --progress-bar "$BASE/$NAME" -o "$TMP/$NAME" || die "download failed: $BASE/$NAME"
  curl -fsSL --retry 3 "$BASE/SHA256SUMS" | grep " $NAME\$" > "$TMP/SHA256SUMS" || die "could not fetch checksums"
fi
if [ -s "$TMP/SHA256SUMS" ]; then
  (cd "$TMP" && sha256sum -c --quiet SHA256SUMS) || die "checksum mismatch — download corrupted or tampered"
  ok "checksum verified"
else
  printf '%s! no SHA256SUMS next to the binary; skipping verification%s\n' "$c_dim" "$c_0"
fi
chmod +x "$TMP/$NAME"
"$TMP/$NAME" version >/dev/null || die "the binary does not run on this system"

# 2. System ---------------------------------------------------------------
step "loading kernel modules"
MODULES="sit ipip ip6_tunnel ip_gre ip6_gre vxlan geneve fou"
for m in $MODULES; do modprobe "$m" 2>/dev/null || true; done
printf '%s\n' $MODULES > /etc/modules-load.d/neotunnel.conf
if ! command -v nft >/dev/null; then
  step "installing nftables"
  (apt-get update -qq && apt-get install -y -qq nftables >/dev/null) || printf '%s! could not install nftables; port forwarding needs it%s\n' "$c_dim" "$c_0"
fi

UPGRADE=0
[ -x /usr/local/bin/neotunnel ] && UPGRADE=1
install -m 755 "$TMP/$NAME" /usr/local/bin/neotunnel
ok "neotunnel $(neotunnel version) installed at /usr/local/bin/neotunnel"

[ "$OPTIMIZE" = 1 ] && { neotunnel optimize >/dev/null 2>&1 && ok "network tuned (BBR, fq, buffers)" || true; }

# 3. Panel ----------------------------------------------------------------
CREDS=""
if [ "$PANEL" = 1 ]; then
  if neotunnel panel info >/dev/null 2>&1 && [ -z "$PORT" ]; then
    ok "panel already configured; keeping its port and password"
  else
    ARGS=()
    [ -n "$PORT" ] && ARGS+=(--port "$PORT")
    CREDS=$(NO_COLOR=1 neotunnel panel setup "${ARGS[@]}") || die "panel setup failed"
  fi
fi

# 4. Service --------------------------------------------------------------
neotunnel install >/dev/null
ok "service running (systemd: neotunnel)"

echo
if [ "$PANEL" = 1 ]; then
  if [ -n "$CREDS" ]; then
    printf '%s\n' "$CREDS" | sed -n '/address\|username\|password/p'
    echo
    printf '%s  Save the password now — it is shown only once (reset: sudo neotunnel panel password).%s\n' "$c_dim" "$c_0"
  else
    NO_COLOR=1 neotunnel panel info | sed -n '/address\|username/p'
  fi
  printf '%s  The certificate is self-signed: accept the browser warning once.%s\n' "$c_dim" "$c_0"
else
  printf '  Destination server ready. Run the %sneotunnel apply%s command from your main server'"'"'s panel.\n' "$c_b" "$c_0"
fi
[ "$UPGRADE" = 1 ] && ok "upgrade complete; configuration kept"
printf '\n  Menu: %ssudo neotunnel%s    Status: %ssudo neotunnel status%s\n\n' "$c_b" "$c_0" "$c_b" "$c_0"
