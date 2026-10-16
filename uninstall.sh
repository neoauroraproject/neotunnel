#!/usr/bin/env bash
# Removes NeoTunnel completely: service, tunnels, forwarding rules, kernel
# tuning, configuration and the program.
#
#   curl -fsSL https://raw.githubusercontent.com/neoauroraproject/neotunnel/main/uninstall.sh | sudo bash
#
# Add "-s -- --keep-config" to keep /etc/neotunnel (keys, tunnels, panel).
set -euo pipefail

KEEP=0
[ "${1:-}" = "--keep-config" ] && KEEP=1
[ "$(id -u)" -eq 0 ] || { echo "run as root: ... | sudo bash" >&2; exit 1; }

if [ -x /usr/local/bin/neotunnel ]; then
  if [ "$KEEP" = 1 ]; then
    /usr/local/bin/neotunnel uninstall -y --keep-config || true
  else
    /usr/local/bin/neotunnel uninstall -y || true
  fi
else
  systemctl disable --now neotunnel 2>/dev/null || true
  rm -f /etc/systemd/system/neotunnel.service
  systemctl daemon-reload 2>/dev/null || true
  command -v nft >/dev/null && { nft delete table inet neotunnel 2>/dev/null; nft delete table inet neotunnel_ipsec 2>/dev/null; } || true
  while ip rule del pref 7700 2>/dev/null; do :; done
  while ip -6 rule del pref 7700 2>/dev/null; do :; done
  for l in $(ip -o link show 2>/dev/null | awk -F': ' '$2 ~ /^ntx?-/ {print $2}' | cut -d@ -f1); do ip link del "$l" 2>/dev/null || true; done
  [ "$KEEP" = 1 ] || rm -rf /etc/neotunnel /etc/sysctl.d/90-neotunnel.conf /etc/modules-load.d/neotunnel.conf
fi
rm -f /usr/local/bin/neotunnel
echo "NeoTunnel removed."
[ "$KEEP" = 1 ] && echo "Configuration kept in /etc/neotunnel."
exit 0
