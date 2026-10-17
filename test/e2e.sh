#!/usr/bin/env bash
# End-to-end test on a real kernel: two "servers" (network namespaces) joined
# by a veth link, plus a client namespace. Runs the actual user flow: panel
# setup, quick 6to4, setup key on the peer, automatic pairing and pushes,
# port-based (FOU) tunnels, offline catch-up, tests, bench, port forwarding
# (including a remapped port) and cleanup.
#
# Needs root, iproute2, nftables, curl, python3.  Usage: sudo test/e2e.sh ./neotunnel
set -euo pipefail

BIN=$(realpath "${1:?usage: e2e.sh <neotunnel binary>}")
WORK=$(mktemp -d)
HUB=nt-e2e-hub EDGE=nt-e2e-edge CLIENT=nt-e2e-client
HUB_IP=192.0.2.1 EDGE_IP=192.0.2.2
PANEL_PORT=34567 PANEL_PASS=e2e-password-123

pass() { printf '\033[32mPASS\033[0m %s\n' "$*"; }
fail() {
  printf '\033[31mFAIL\033[0m %s\n' "$*"
  echo "--- hub daemon log";   tail -n 40 "$WORK/hub.log" 2>/dev/null || true
  echo "--- edge daemon log"; tail -n 40 "$WORK/edge.log" 2>/dev/null || true
  exit 1
}
in_hub()   { ip netns exec $HUB   env NEOTUNNEL_DIR=$WORK/hub   NO_COLOR=1 "$BIN" "$@"; }
in_edge() { ip netns exec $EDGE env NEOTUNNEL_DIR=$WORK/edge NO_COLOR=1 "$BIN" "$@"; }
start_edge() { ip netns exec $EDGE env NEOTUNNEL_DIR=$WORK/edge "$BIN" daemon >>"$WORK/edge.log" 2>&1 & echo $! > "$WORK/edge.pid"; sleep 1; }
http_code() { ip netns exec $CLIENT curl -s -o /dev/null -m 5 -w '%{http_code}' "$1" || true; }

cleanup() {
  set +e
  for p in $(jobs -p); do kill "$p" 2>/dev/null; done
  [ -f "$WORK/edge.pid" ] && kill "$(cat "$WORK/edge.pid")" 2>/dev/null
  for ns in $HUB $EDGE $CLIENT; do ip netns del $ns 2>/dev/null; done
  rm -rf "$WORK"
}
trap cleanup EXIT

for m in sit ipip ip6_tunnel ip_gre ip6_gre vxlan geneve fou wireguard tun l2tp_eth l2tp_ip xfrm_user esp4 esp6; do modprobe $m 2>/dev/null || true; done

ip netns add $HUB; ip netns add $EDGE; ip netns add $CLIENT
ip link add veth-hub netns $HUB type veth peer name veth-edge netns $EDGE
ip -n $HUB addr add $HUB_IP/24 dev veth-hub
ip -n $EDGE addr add $EDGE_IP/24 dev veth-edge
ip link add veth-c netns $CLIENT type veth peer name veth-ci netns $HUB
ip -n $CLIENT addr add 198.18.0.2/24 dev veth-c
ip -n $HUB addr add 198.18.0.1/24 dev veth-ci
for ns in $HUB $EDGE $CLIENT; do
  ip -n $ns link set lo up
  for dev in $(ip -n $ns -o link show type veth | awk -F': ' '{print $2}' | cut -d@ -f1); do ip -n $ns link set "$dev" up; done
done

# A web server on edge stands in for the real service behind the tunnel.
mkdir -p "$WORK/www" && echo hello-from-edge > "$WORK/www/index.html"
ip netns exec $EDGE python3 -m http.server 8080 --directory "$WORK/www" >/dev/null 2>&1 &

in_hub node --name hub --address $HUB_IP >/dev/null
in_edge node --name edge --address $EDGE_IP >/dev/null
in_hub panel setup --port $PANEL_PORT --password $PANEL_PASS >/dev/null || fail "panel setup"
ip netns exec $HUB env NEOTUNNEL_DIR=$WORK/hub "$BIN" daemon >"$WORK/hub.log" 2>&1 &
start_edge
sleep 1

# 1. Quick 6to4 while the peer is not paired: the master prints its commands.
out=$(in_hub quick edge --address $EDGE_IP --forward 8080 --proto tcp)
echo "$out"
key=$(grep -o "NT1\.[^']*" <<<"$out" | head -1)
[ -n "$key" ] || fail "no setup key printed"
grep -q "curl -fsSk https://$HUB_IP:7710/dl/" <<<"$out" || fail "no install one-liner"
pass "main server printed the setup key and install one-liner"

url=$(grep -o "https://$HUB_IP:7710/dl/[0-9a-f]*" <<<"$out" | head -1)
sum=$(grep -o "echo '[0-9a-f]*" <<<"$out" | head -1 | cut -c7-)
ip netns exec $EDGE curl -fsSk "$url" -o "$WORK/dl" || fail "binary download"
echo "$sum  $WORK/dl" | sha256sum -c --quiet || fail "checksum of served binary"
pass "binary served by master with matching checksum"

# 2. The peer applies the setup key: tunnels come up and it pairs.
in_edge apply "$key"
in_hub peer list | grep -q "edge.*in sync" || fail "peer not paired and in sync"
pass "peer paired and in sync"

ip -n $HUB -d link show nt-t1-6 | grep -q sit || fail "sit missing on hub"
ip -n $EDGE -d link show nt-t1 | grep -q ip6tnl || fail "ipip6 missing on edge"
ip netns exec $HUB ping -c 3 -W 2 10.77.0.2 >/dev/null || fail "ping through 6to4 (IPv4 over ipip6 over sit)"
pass "6to4 stack up, IPv4 ping through it works"

# 3. Changes on the master reach the peer by themselves.
out=$(in_hub tunnel add v1 --kind vxlan --peer edge 2>&1)
grep -q "edge updated" <<<"$out" || { echo "$out"; fail "push to peer"; }
ip -n $EDGE -d link show nt-v1 | grep -q vxlan || fail "vxlan not created on peer"
v1peer=$(ip -n $EDGE -o -4 addr show dev nt-v1 | awk '{print $4}' | cut -d/ -f1)
ip netns exec $HUB ping -c 3 -W 2 "$v1peer" >/dev/null || fail "ping through vxlan"
in_hub tunnel remove v1 >/dev/null
ip -n $EDGE link show nt-v1 >/dev/null 2>&1 && fail "vxlan still on peer after remove"
pass "add and remove pushed automatically (vxlan)"

# 3b. The interactive menu, driven through a real pseudo-terminal: create a
#     VXLAN tunnel to edge (1 → edge, kind 9 = vxlan, no protection, no
#     port, no forward, name m1, confirm), then quit.
keys() { for k in "$@"; do printf '%b' "$k"; sleep 0.7; done; }
keys '1' '1' '9' '\r' '\r' '\r' 'm1\r' 'y\r' ' ' 'q' |
  ip netns exec $HUB env NEOTUNNEL_DIR=$WORK/hub TERM=xterm script -qfec "$BIN" /dev/null > "$WORK/menu.log" 2>&1 || true
grep -q "menu closed" "$WORK/menu.log" || { tail -c 3000 "$WORK/menu.log"; fail "menu did not exit cleanly"; }
ip -n $EDGE -d link show nt-m1 | grep -q vxlan || { tail -c 3000 "$WORK/menu.log"; fail "tunnel created in the menu missing on edge"; }
in_hub tunnel remove m1 >/dev/null
pass "menu: created a VXLAN tunnel on both servers with the keyboard"

# 4. Port-based tunnel: IPIP inside UDP 3001 (FOU), plus a remapped forward.
in_hub tunnel add u1 --kind ipip --peer edge --port 3001 >/dev/null || fail "add ipip on port 3001"
ip -n $HUB -d link show nt-u1 | grep -q "encap fou" || fail "nt-u1 is not FOU-encapsulated"
ip netns exec $EDGE ip fou show | grep -q "port 3001" || fail "peer has no FOU listener on 3001"
u1peer=$(ip -n $EDGE -o -4 addr show dev nt-u1 | awk '{print $4}' | cut -d/ -f1)
ip netns exec $HUB ping -c 3 -W 2 "$u1peer" >/dev/null || fail "ping through ipip over UDP 3001"
in_hub forward add u1 9090 --to 8080 --proto tcp >/dev/null || fail "add remapped forward"
sleep 16  # one daemon reconcile with the FOU port already open must stay quiet
grep -q "UDP port 3001" "$WORK/hub.log" && fail "daemon reports the open FOU port as a problem"
pass "IPIP on UDP port 3001 works on both ends"

# 5. Offline catch-up: stop the peer, change things, apply one setup key.
kill "$(cat "$WORK/edge.pid")"; sleep 0.5
out=$(in_hub tunnel add g1 --kind gre --peer edge)
grep -q "not reachable" <<<"$out" || fail "expected unreachable peer"
key=$(grep -o "NT1\.[^']*" <<<"$out" | head -1)
in_edge apply "$key" >/dev/null
ip -n $EDGE -d link show nt-g1 | grep -q gre || fail "gre not applied from the offline setup key"
start_edge
pass "offline peer caught up with one command"

# 6. Port forwarding from a client: 8080 via 6to4, 9090 -> 8080 via UDP tunnel.
[ "$(http_code http://198.18.0.1:8080/)" = 200 ] || fail "forward 8080 through 6to4"
[ "$(http_code http://198.18.0.1:9090/)" = 200 ] || fail "forward 9090 -> 8080 through ipip/udp"
pass "ports 8080 and 9090(->8080) reach edge through two different tunnels"

# 7. Built-in test and benchmark.
in_hub test t1 --duration 2s --pings 30 | tee "$WORK/test.txt"
grep -q "Mbit/s\|Gbit/s" "$WORK/test.txt" || fail "test produced no throughput"
pass "tunnel test (ping, jitter, loss, bandwidth)"
in_hub bench edge --duration 1s --pings 20 | tee "$WORK/bench.txt"
grep -q "fastest tunnel" "$WORK/bench.txt" || fail "bench gave no recommendation"
sleep 1
ip -n $HUB -o link | grep -qE '^[0-9]+: ntx-' && fail "bench left temporary tunnels on master"
ip -n $EDGE -o link | grep -qE '^[0-9]+: ntx-' && fail "bench left temporary tunnels on peer"
pass "benchmark ranked the kinds and cleaned up"

# 8. Panel: login, overview, create a tunnel through the API.
purl=$(in_hub panel info | grep -o "https://[^ ]*")
ppath=$(sed -E 's#https://[^/]+(/[^/]+)/#\1#' <<<"$purl")
base="https://127.0.0.1:$PANEL_PORT$ppath"
jar="$WORK/cookies"
api() { ip netns exec $HUB curl -sk -b "$jar" -c "$jar" -H 'Content-Type: application/json' -H 'X-NeoTunnel: 1' "$@"; }
[ "$(api -o /dev/null -w '%{http_code}' "$base/api/overview")" = 401 ] || fail "panel API open without login"
api -d "{\"user\":\"admin\",\"password\":\"wrong\"}" "$base/api/login" | grep -q error || fail "panel accepted a wrong password"
api -d "{\"user\":\"admin\",\"password\":\"$PANEL_PASS\"}" "$base/api/login" | grep -q '"ok":true' || fail "panel login"
api "$base/api/overview" | grep -q '"name":"t1"' || fail "overview misses tunnel t1"
api -d '{"peer":"edge","mode":"gretap","name":"p1"}' "$base/api/tunnels" | grep -q '"ok":true' || fail "create tunnel via panel"
ip -n $EDGE -d link show nt-p1 | grep -q gretap || fail "panel-created tunnel missing on peer"
[ "$(ip netns exec $HUB curl -sk -o /dev/null -w '%{http_code}' "https://127.0.0.1:$PANEL_PORT/")" = 404 ] || fail "panel reachable outside its secret path"
pass "panel: auth, overview and tunnel creation reach the peer"

# 10. IPsec: an encrypted GRE tunnel; its packets travel as ESP.
inner() { ip -n "$1" -o -4 addr show dev "$2" | awk '{print $4}' | cut -d/ -f1; }
hup() { pkill -HUP -f "neotunnel daemon" || true; sleep "${1:-2}"; }
out=$(in_hub tunnel add e1 --kind gre --peer edge --encrypt aes-gcm-128 2>&1) || { echo "$out"; fail "add encrypted gre"; }
ip netns exec $HUB ip xfrm state | grep -q "spi 0x4e" || fail "no ESP SA on hub"
ip netns exec $EDGE ip xfrm state | grep -q "spi 0x4e" || fail "no ESP SA on edge"
[ "$(ip -n $HUB -o link show nt-e1 | grep -o 'mtu [0-9]*')" = "mtu 1434" ] || fail "encrypted gre MTU should be 1434"
ip netns exec $HUB ping -c 3 -W 2 "$(inner $EDGE nt-e1)" >/dev/null || fail "ping through encrypted gre"
ip netns exec $HUB ip -s xfrm state | grep -qE '[1-9][0-9]*\(bytes\)' || fail "ESP SAs carried no traffic"
pass "IPsec (AES-GCM) encrypts the GRE tunnel; MTU accounts for ESP"

# 11. WireGuard with keys derived on both ends.
in_hub tunnel add w1 --kind wireguard --peer edge >/dev/null || fail "add wireguard"
ip -n $EDGE -d link show nt-w1 | grep -q wireguard || fail "wireguard missing on edge"
ip netns exec $HUB ping -c 3 -W 2 "$(inner $EDGE nt-w1)" >/dev/null || fail "ping through wireguard"
pass "WireGuard tunnel up with derived keys"

# 12. NeoObfs, alone and under a kernel tunnel (runs in the daemons).
in_hub tunnel add o1 --kind obfs --peer edge --port 8443 --padding 16 >/dev/null || fail "add obfs"
in_hub quick edge --name q2 --mode ipip --obfs --port 8444 >/dev/null || fail "quick ipip over obfs"
hup 3
ip -n $HUB link show nt-o1 >/dev/null 2>&1 || fail "obfs interface missing on hub"
ip netns exec $HUB ping -c 3 -W 2 "$(inner $EDGE nt-o1)" >/dev/null || fail "ping through NeoObfs"
ip netns exec $HUB ping -c 3 -W 2 "$(inner $EDGE nt-q2)" >/dev/null || fail "ping through ipip over NeoObfs"
pass "NeoObfs carries traffic, and kernel tunnels stack on it"

# 12b. TCP transport (user space): plain and with a stream cipher, a speed
#      test through it, recovery when the far daemon restarts, and the
#      panel's check, which brings a temporary TCP tunnel up in both daemons.
in_hub tunnel add c1 --kind tcp --peer edge --port 8445 >/dev/null || fail "add tcp"
in_hub tunnel add c2 --kind tcp --peer edge --port 8446 --encrypt chacha20-poly1305 >/dev/null || fail "add encrypted tcp"
hup 3
for n in c1 c2; do
  ip -n $EDGE link show nt-$n >/dev/null 2>&1 || fail "tcp interface nt-$n missing on edge"
  ip netns exec $HUB ping -c 3 -W 2 "$(inner $EDGE nt-$n)" >/dev/null || fail "ping through tcp tunnel $n"
done
ip netns exec $HUB ss -Htn state established "( sport = :8446 or dport = :8446 )" | grep -q . || fail "no TCP connection on port 8446"
in_hub test c2 --duration 2s --pings 20 | tee "$WORK/tcptest.txt"
grep -q "Mbit/s\|Gbit/s" "$WORK/tcptest.txt" || fail "no throughput through the tcp tunnel"
kill "$(cat "$WORK/edge.pid")"; sleep 0.5
start_edge
ok=
for _ in $(seq 15); do ip netns exec $HUB ping -c 1 -W 1 "$(inner $EDGE nt-c2)" >/dev/null 2>&1 && { ok=1; break; }; done
[ -n "$ok" ] || fail "tcp tunnel did not reconnect after the edge daemon restarted"
api -d '{"peer":"edge","mode":"tcp","encrypt":"aes-gcm-128"}' "$base/api/check" | tee "$WORK/tcpcheck.json" | grep -q '"status":"passed"' || { cat "$WORK/tcpcheck.json"; fail "panel check of a tcp tunnel"; }
pass "TCP transport carries ping and bandwidth, reconnects, and passes the panel check"

# 13. L2TPv3, where the kernel has it.
if out=$(in_hub tunnel add l1 --kind l2tpv3 --peer edge 2>&1) && ip -n $HUB link show nt-l1 >/dev/null 2>&1; then
  ip netns exec $HUB ping -c 3 -W 2 "$(inner $EDGE nt-l1)" >/dev/null || fail "ping through l2tpv3"
  in_hub tunnel remove l1 >/dev/null
  pass "L2TPv3 pseudowire"
else
  echo "SKIP L2TPv3: $(grep -o 'l2tp[^"]*' <<<"$out" | head -1) (no kernel support here)"
  in_hub tunnel remove l1 >/dev/null 2>&1 || true
fi

# 13b. EoIP (MikroTik's Ethernet over IP, user space TAP): raw GRE and in UDP.
if out=$(in_hub tunnel add eo1 --kind eoip --peer edge 2>&1) && hup 3 && ip -n $HUB link show nt-eo1 >/dev/null 2>&1 && ip -n $EDGE link show nt-eo1 >/dev/null 2>&1; then
  ip -n $HUB -d link show nt-eo1 | grep -q "tun type tap" || fail "eoip is not a TAP interface"
  ip netns exec $HUB ping -c 3 -W 2 "$(inner $EDGE nt-eo1)" >/dev/null || fail "ping through EoIP"
  ip -n $HUB neigh show dev nt-eo1 | grep -q lladdr || fail "no layer-2 neighbor over EoIP"
  in_hub tunnel add eo2 --kind eoip --peer edge --port 8450 >/dev/null || fail "add eoip over udp"
  hup 3
  ip netns exec $HUB ping -c 3 -W 2 "$(inner $EDGE nt-eo2)" >/dev/null || fail "ping through EoIP over UDP"
  in_hub tunnel remove eo1 >/dev/null; in_hub tunnel remove eo2 >/dev/null
  pass "EoIP carries Ethernet over GRE and over UDP"
else
  echo "SKIP EoIP: TAP interface did not come up here ($(tail -n 1 <<<"$out"))"
  in_hub tunnel remove eo1 >/dev/null 2>&1 || true
fi

# 14. Transparent forward: the service on edge sees the client's real IP,
#     and its replies find their way back through the tunnel.
ip netns exec $EDGE python3 -m http.server 8081 --directory "$WORK/www" >"$WORK/www8081.log" 2>&1 &
sleep 1
in_hub forward add t1 8081 --proto tcp --mode transparent >/dev/null || fail "add transparent forward"
[ "$(http_code http://198.18.0.1:8081/)" = 200 ] || fail "transparent forward"
grep -q "198.18.0.2" "$WORK/www8081.log" || { cat "$WORK/www8081.log"; fail "edge did not see the client's address"; }
pass "transparent forward: edge saw the client's real IP"

# 15. PROXY protocol: the relay tells the service who the client is.
ip netns exec $EDGE python3 -c '
import socket, sys
s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("0.0.0.0", 9100)); s.listen(1)
c, _ = s.accept(); d = b""
while True:
    x = c.recv(4096)
    if not x: break
    d += x
open(sys.argv[1], "wb").write(d)' "$WORK/proxy.txt" &
in_hub forward add t1 9101 --to 9100 --proto tcp --mode proxy --proxy-version 1 >/dev/null || fail "add proxy forward"
hup 2
ip netns exec $CLIENT bash -c 'exec 3<>/dev/tcp/198.18.0.1/9101; printf hello >&3; exec 3>&-' || fail "connect to proxy forward"
sleep 1
grep -q "^PROXY TCP4 198.18.0.2 198.18.0.1 [0-9]* 9101" "$WORK/proxy.txt" || { cat "$WORK/proxy.txt"; fail "no PROXY header"; }
grep -q "hello" "$WORK/proxy.txt" || fail "payload after the PROXY header missing"
pass "PROXY protocol v1 header carries the client's address"

# 16. Panel: traffic totals, MTU measurement, changing a tunnel's settings.
sleep 11  # one traffic sample
api "$base/api/traffic" | grep -q '"t1"' || fail "no traffic totals for t1"
api -X POST "$base/api/tunnels/t1/mtu" | grep -q '"path_mtu":1500' || fail "MTU probe"
api -X PATCH -d '{"encrypt":"aes-gcm-256"}' "$base/api/tunnels/e1" | grep -q error && fail "update tunnel settings"
ip netns exec $EDGE ip xfrm state | grep -q "rfc4106" || fail "re-keyed SA missing on edge"
ip netns exec $HUB ping -c 3 -W 2 "$(inner $EDGE nt-e1)" >/dev/null || fail "ping after switching to AES-256"
pass "panel: traffic totals, MTU probe and live settings change"

# 17. Uninstall from the command line removes everything, the program too.
kill "$(cat "$WORK/edge.pid")"; sleep 0.5
cp "$BIN" "$WORK/nt-edge"
ip netns exec $EDGE env NEOTUNNEL_DIR=$WORK/edge NO_COLOR=1 "$WORK/nt-edge" uninstall -y >/dev/null
[ -e "$WORK/nt-edge" ] && fail "uninstall left the program"
[ -d "$WORK/edge" ] && fail "uninstall left the configuration"
ip -n $EDGE -o link | grep -qE '^[0-9]+: nt-' && fail "uninstall left interfaces"
ip netns exec $EDGE ip xfrm state | grep -q "spi 0x4e" && fail "uninstall left IPsec SAs"
ip netns exec $EDGE ip rule | grep -q "^7700:" && fail "uninstall left routing rules"
pass "uninstall removed tunnels, IPsec, rules, configuration and the program"

# 9. Clean removal.
in_hub down >/dev/null
ip -n $HUB -o link | grep -qE '^[0-9]+: nt-' && fail "interfaces left after down"
ip netns exec $HUB ip fou show | grep -q "port 3001" && fail "FOU port left after down"
pass "down removes interfaces and FOU ports"

echo
pass "all e2e checks"
