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
  for ns in $HUB $EDGE $CLIENT nt-e2e-hub2; do ip netns del $ns 2>/dev/null; done
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
ip netns exec $HUB env NEOTUNNEL_DIR=$WORK/hub NEOTUNNEL_PEER_WATCH=3s "$BIN" daemon >"$WORK/hub.log" 2>&1 & echo $! > "$WORK/hub.pid"
start_edge
sleep 1

# 1. Quick 6to4 while the peer is not paired: the master prints its commands.
out=$(in_hub quick edge --address $EDGE_IP --forward 8080 --proto tcp)
echo "$out"
key=$(grep -o "NT1\.[^']*" <<<"$out" | head -1)
[ -n "$key" ] || fail "no setup key printed"
grep -Eq "curl -fsSk https://$HUB_IP:[0-9]+/dl/" <<<"$out" || fail "no install one-liner"
grep -q "https://$HUB_IP:7710/dl/" <<<"$out" && fail "the install one-liner still uses the fixed port 7710"
pass "main server printed the setup key and install one-liner"

url=$(grep -Eo "https://$HUB_IP:[0-9]+/dl/[0-9a-f]*" <<<"$out" | head -1)
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

# 2b. Display names: renaming changes only the label, on both servers; the
#     running tunnel (same interfaces) keeps passing traffic.
idx_hub=$(ip -n $HUB -o link show nt-t1 | cut -d: -f1)
idx_edge=$(ip -n $EDGE -o link show nt-t1 | cut -d: -f1)
in_hub tunnel rename t1 "آلمان اصلی" >/dev/null || fail "rename tunnel"
in_hub peer rename edge "سرور مقصد" >/dev/null || fail "rename peer"
in_hub node --label "ایران" >/dev/null || fail "rename this node"
in_hub tunnel list | grep -q "آلمان اصلی" || fail "tunnel list shows no display name"
grep -q '"label": "آلمان اصلی"' "$WORK/edge/state.json" || fail "the tunnel's display name did not reach edge"
[ "$(ip -n $HUB -o link show nt-t1 | cut -d: -f1)" = "$idx_hub" ] || fail "renaming recreated nt-t1 on hub"
[ "$(ip -n $EDGE -o link show nt-t1 | cut -d: -f1)" = "$idx_edge" ] || fail "renaming recreated nt-t1 on edge"
ip netns exec $HUB ping -c 2 -W 2 10.77.0.2 >/dev/null || fail "ping after renaming"
in_hub peer list | grep -q "edge.*in sync" || fail "peer not in sync after renaming"
pass "display names: tunnel, peer and node renamed; the tunnel stays up on the same interfaces"

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

# 5b. Automatic catch-up: a change made while the peer is down reaches it
#     once it is back, without any command (the hub's peer watch resends).
#     The peer's agent listens on a port of its own, which the hub learns.
kill "$(cat "$WORK/edge.pid")"; sleep 0.5
in_hub tunnel add g9 --kind gre --peer edge >/dev/null 2>&1 || true
ip -n $EDGE link show nt-g9 >/dev/null 2>&1 && fail "g9 reached a stopped peer?"
start_edge
for i in $(seq 1 20); do ip -n $EDGE -d link show nt-g9 2>/dev/null | grep -q gre && break; sleep 1; done
ip -n $EDGE -d link show nt-g9 2>/dev/null | grep -q gre || fail "the peer did not catch up by itself"
eport=$(grep -o '"agent_port": *[0-9]*' "$WORK/edge/state.json" | head -1 | grep -o '[0-9]*$')
[ -n "$eport" ] && [ "$eport" != 7710 ] || fail "edge has no agent port of its own ($eport)"
ip netns exec $EDGE ss -ltn | grep -q ":$eport " || fail "edge's agent does not listen on $eport"
grep -q "\"agent_port\": *$eport" "$WORK/hub/state.json" || fail "hub did not learn edge's agent port $eport"
in_hub tunnel remove g9 >/dev/null
pass "peer caught up automatically; its agent runs on its own port $eport"

# 6. Port forwarding from a client: 8080 via 6to4, 9090 -> 8080 via UDP tunnel.
[ "$(http_code http://198.18.0.1:8080/)" = 200 ] || fail "forward 8080 through 6to4"
[ "$(http_code http://198.18.0.1:9090/)" = 200 ] || fail "forward 9090 -> 8080 through ipip/udp"
pass "ports 8080 and 9090(->8080) reach edge through two different tunnels"

# 6b. A program on hub itself (an Xray outbound to 127.0.0.1:port) takes the
#     same forwards: through loopback, hub's public and its tunnel address.
for a in 127.0.0.1 $HUB_IP 10.77.0.1; do
  ip netns exec $HUB curl -s -m 5 "http://$a:8080/" | grep -q hello-from-edge || fail "forward 8080 from hub itself via $a"
done
ip netns exec $HUB curl -s -m 5 http://127.0.0.1:9090/ | grep -q hello-from-edge || fail "forward 9090 -> 8080 from hub itself"
[ "$(ip netns exec $HUB cat /proc/sys/net/ipv4/conf/nt-t1/route_localnet)" = 1 ] || fail "route_localnet not on for nt-t1"
pass "forwards also serve hub's own programs at 127.0.0.1, its public and its tunnel address"

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
out=$(in_hub tunnel add w1 --kind wireguard --peer edge 2>&1) || { echo "$out"; fail "add wireguard"; }
ip -n $EDGE -d link show nt-w1 2>/dev/null | grep -q wireguard || { echo "$out"; fail "wireguard missing on edge"; }
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

# 12b'. A comparison whose test port is taken on the far end moves to a free
#       port instead of failing (another program holds TCP 40420 on edge).
ip netns exec $EDGE python3 -c 'import socket,time; s=socket.socket(); s.bind(("0.0.0.0",40420)); s.listen(); time.sleep(120)' &
busy=$!
sleep 0.5
job=$(api -d '{"kind":"bench","target":"edge","only":["tcp"],"seconds":3,"streams":2}' "$base/api/jobs" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])') || fail "start a comparison from the panel"
for _ in $(seq 90); do
  api "$base/api/jobs/$job" > "$WORK/job.json"
  python3 -c 'import json,sys; sys.exit(0 if json.load(open(sys.argv[1]))["status"] != "running" else 1)' "$WORK/job.json" && break
  sleep 1
done
python3 - "$WORK/job.json" <<'EOF' || { cat "$WORK/job.json"; fail "comparison with a busy test port"; }
import json, sys
j = json.load(open(sys.argv[1]))
r = [x for x in j["results"] if x["name"] == "tcp"][0]
assert j["status"] == "done", j["status"]
assert r.get("status") == "done", r
assert r.get("moved_from") == 40420 and r.get("port") not in (None, 40420), r
assert r["down_bps"] > 0 and r["up_bps"] > 0, r
EOF
kill $busy 2>/dev/null
pass "TCP comparison moved off the busy port 40420 and measured both directions"

# 12c. Lanes: one GRE tunnel on 4 parallel GRE tunnels (distinct keys), the
#      far end's address reached by a multipath route over all of them; a
#      speed test, a NAT and a transparent forward through it, then back to
#      one lane.
in_hub tunnel add g4 --kind gre --peer edge --lanes 4 >/dev/null || fail "add gre on 4 lanes"
for ns in $HUB $EDGE; do
  for i in "" .1 .2 .3; do ip -n $ns -d link show "nt-g4$i" | grep -q "gre remote" || fail "lane nt-g4$i missing in $ns"; done
done
g4far=$(ip -n $EDGE -o -4 addr show dev nt-g4 | awk '{print $4}' | cut -d/ -f1)
[ "$(ip -n $EDGE -o -4 addr show dev nt-g4.3 | awk '{print $4}')" = "$g4far/32" ] || fail "lanes do not share the inner address"
[ "$(ip -n $HUB route show "$g4far" | grep -c 'nexthop')" = 4 ] || { ip -n $HUB route show "$g4far"; fail "no 4-way multipath route to $g4far"; }
ip netns exec $HUB ping -c 3 -W 2 "$g4far" >/dev/null || fail "ping through gre on lanes"
# IPv6 has no device-only multipath routes: its next hop is the far side's
# lane link-local address on every lane.
g4far6=$(ip -n $EDGE -o -6 addr show dev nt-g4 scope global | awk '{print $4}' | cut -d/ -f1)
[ -n "$g4far6" ] || fail "no inner IPv6 address on lanes"
[ "$(ip -n $HUB -6 route show "$g4far6" | grep -c 'nexthop via fe80::4e54:')" = 4 ] || { ip -n $HUB -6 route show "$g4far6"; fail "no 4-way IPv6 multipath route to $g4far6"; }
ip netns exec $HUB ping -6 -c 3 -W 2 "$g4far6" >/dev/null || fail "IPv6 ping through gre on lanes"
in_hub test g4 --duration 2s --pings 20 | tee "$WORK/lanetest.txt"
grep -q "Mbit/s\|Gbit/s" "$WORK/lanetest.txt" || fail "no throughput through the lanes"
used=0
for i in "" .1 .2 .3; do
  rx=$(ip netns exec $EDGE cat "/sys/class/net/nt-g4$i/statistics/rx_packets")
  [ "$rx" -gt 100 ] && used=$((used + 1))
done
[ "$used" -ge 2 ] || fail "the speed test used only $used lane(s)"
in_hub forward add g4 8082 --to 8080 --proto tcp >/dev/null || fail "add forward over lanes"
[ "$(http_code http://198.18.0.1:8082/)" = 200 ] || fail "forward through gre on lanes"
in_hub forward add g4 8083 --to 8080 --proto tcp --mode transparent >/dev/null || fail "add transparent forward over lanes"
for _ in 1 2 3 4 5 6; do [ "$(http_code http://198.18.0.1:8083/)" = 200 ] || fail "transparent forward through lanes"; done
in_hub tunnel set g4 --lanes 1 >/dev/null || fail "back to one lane"
ip -n $EDGE link show nt-g4.1 >/dev/null 2>&1 && fail "lane nt-g4.1 left after going to one lane"
ip -n $HUB route show proto 77 | grep -q . && fail "lane route left after going to one lane"
ip netns exec $HUB ping -c 3 -W 2 "$g4far" >/dev/null || fail "ping after going back to one lane"
[ "$(http_code http://198.18.0.1:8082/)" = 200 ] || fail "forward after going back to one lane"
in_hub tunnel remove g4 >/dev/null
pass "lanes: GRE on 4 parallel tunnels carries ping, a speed test and forwards; back to one lane cleans up"

# 12d. The max profile spreads the card's receive work: every receive queue
#      of the card holding hub's address hands its packets on to other cores
#      (RPS, by the inner flow), the tunnel then leaves its own RPS off, and
#      the doctor says so.
if [ "$(nproc)" -gt 1 ]; then
  in_hub tunnel add g5 --kind gre --peer edge >/dev/null || fail "add gre for the max profile"
  in_hub tunnel set g5 --profile max >/dev/null || fail "set the max profile"
  for q in $(ip netns exec $HUB sh -c 'ls -d /sys/class/net/veth-hub/queues/rx-*'); do
    m=$(ip netns exec $HUB cat "$q/rps_cpus")
    [ -n "$(echo "$m" | tr -d '0,')" ] || fail "max profile: $q of veth-hub does not spread (rps_cpus $m)"
  done
  m=$(ip netns exec $HUB cat /sys/class/net/nt-g5/queues/rx-0/rps_cpus)
  [ -z "$(echo "$m" | tr -d '0,')" ] || fail "max profile: nt-g5 spreads again on top of the card (rps_cpus $m)"
  ip netns exec $HUB ping -c 3 -W 2 "$(inner $EDGE nt-g5)" >/dev/null || fail "ping through gre with the max profile"
  doc=$(in_hub doctor 2>&1 || true)
  grep -q "✓ RPS on veth-hub: 1 of 1" <<<"$doc" || { echo "$doc"; fail "doctor does not report how the card spreads"; }
  in_hub tunnel remove g5 >/dev/null
  for q in $(ip netns exec $HUB sh -c 'ls -d /sys/class/net/veth-hub/queues/rx-*'); do ip netns exec $HUB sh -c "echo 0 > $q/rps_cpus"; done
  pass "max profile: the card hands tunnel packets on to other cores; the tunnel does not spread twice"
fi

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

# 15b. Relay mode: TCP forwards carried to edge's relay port as their own TCP
#      connections, no tunnel packets. Plain over t1 (a 6to4 stack: edge's
#      address comes from the sit under it), encrypted over e1 (an IPsec
#      tunnel, so the relay uses its cipher), and with a PROXY header.
ip netns exec $EDGE python3 -c '
import socket, sys
s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("127.0.0.1", 9102)); s.listen(1)
c, _ = s.accept(); d = b""
while True:
    x = c.recv(4096)
    if not x: break
    d += x
open(sys.argv[1], "wb").write(d)' "$WORK/relay-proxy.txt" &
in_hub forward add t1 8090 --to 8080 --mode relay >/dev/null || fail "add relay forward"
in_hub forward add e1 8091 --to 8080 --mode relay >/dev/null || fail "add encrypted relay forward"
in_hub forward add t1 9103 --to 9102 --mode relay --proxy-version 1 >/dev/null || fail "add relay forward with PROXY header"
hup 3
[ "$(http_code http://198.18.0.1:8090/)" = 200 ] || fail "relay forward 8090 -> edge 8080"
[ "$(http_code http://198.18.0.1:8091/)" = 200 ] || fail "encrypted relay forward 8091 -> edge 8080"
ip netns exec $CLIENT curl -s -m 5 http://198.18.0.1:8090/ | grep -q hello-from-edge || fail "relay forward content"
ip netns exec $EDGE ss -Htn state established "( sport = :7712 )" | grep -q "$HUB_IP" || fail "no relay connections from hub on edge:7712"
ip netns exec $HUB nft list table inet neotunnel | grep -q "8090" && fail "relay forward must not be a DNAT rule"
ip netns exec $CLIENT bash -c 'exec 3<>/dev/tcp/198.18.0.1/9103; printf hello >&3; exec 3>&-' || fail "connect to relay forward 9103"
sleep 1
grep -q "^PROXY TCP4 198.18.0.2 198.18.0.1 [0-9]* 9103" "$WORK/relay-proxy.txt" || { cat "$WORK/relay-proxy.txt"; fail "no PROXY header through the relay"; }
grep -q "hello" "$WORK/relay-proxy.txt" || fail "payload after the relay's PROXY header missing"
pass "relay mode: plain, encrypted and with the client's address in a PROXY header"

# 15b2. Proxy: a SOCKS5/HTTP proxy on hub for its own programs, whose
#       connections go through the tunnel to edge and leave from there. A
#       "whoami" server stands in for the internet: it must see edge's
#       address, not hub's. Loopback destinations are refused; a proxy open
#       to others needs its password.
ip netns exec $EDGE python3 -c '
import http.server
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        b = self.client_address[0].encode()
        self.send_response(200); self.send_header("Content-Length", str(len(b))); self.end_headers(); self.wfile.write(b)
    def log_message(self, *a): pass
http.server.ThreadingHTTPServer(("0.0.0.0", 8095), H).serve_forever()' &
in_hub forward add t1 1080 --mode socks >/dev/null || fail "add proxy"
in_hub forward add e1 1082 --mode socks --listen 0.0.0.0 --auth "u1:pw-123" >/dev/null || fail "add encrypted proxy with password"
in_hub forward add e1 1083 --mode socks --listen 0.0.0.0 2>/dev/null && fail "open proxy without a password accepted"
hup 3
who() { ip netns exec "$1" curl -s -m 8 "${@:2}" http://$EDGE_IP:8095/ || true; }
[ "$(who $HUB)" = "$HUB_IP" ] || fail "whoami server: direct request should come from hub"
[ "$(who $HUB --socks5-hostname 127.0.0.1:1080)" = "$EDGE_IP" ] || fail "SOCKS5 proxy: request did not leave from edge"
[ "$(who $HUB -x http://127.0.0.1:1080)" = "$EDGE_IP" ] || fail "HTTP proxy (same port): request did not leave from edge"
[ "$(who $CLIENT -x http://u1:pw-123@198.18.0.1:1082)" = "$EDGE_IP" ] || fail "encrypted proxy with password"
[ -z "$(who $CLIENT -x http://u1:wrong@198.18.0.1:1082)" ] || fail "proxy accepted a wrong password"
[ -z "$(who $CLIENT --socks5 198.18.0.1:1080)" ] || fail "loopback-only proxy reachable from the client"
ip netns exec $HUB curl -s -m 5 --socks5-hostname 127.0.0.1:1080 http://127.0.0.1:8080/ >/dev/null && fail "proxy reached edge's loopback"
ip netns exec $EDGE ss -Htn state established "( sport = :7712 )" | grep -vq "$HUB_IP" || fail "proxy connections did not come through the tunnel"
in_hub forward list | grep -q "proxy on 127.0.0.1:1080" || fail "forward list shows the proxy"
in_hub forward remove t1 1080 >/dev/null && in_hub forward remove e1 1082 >/dev/null || fail "remove proxies"
pass "proxy: SOCKS5 and HTTP on one port leave from edge through the tunnel; loopback refused; password enforced"

# 15b3. Downloads through a tunnel: hub's own connections to listed sites
#       are redirected, go through t1 and leave from edge; other sites stay
#       direct. fetch.test, which only edge can reach (hub's route to it is a
#       dead end), stands in for a filtered GitHub.
ip -n $EDGE link add fetch0 type dummy && ip -n $EDGE addr add 203.0.113.80/32 dev fetch0 && ip -n $EDGE link set fetch0 up
ip -n $HUB link add dead0 type dummy && ip -n $HUB link set dead0 up && ip -n $HUB route add 203.0.113.0/24 dev dead0
echo "203.0.113.80 fetch.test other.test" >> /etc/hosts
mkdir -p "$WORK/fetchwww" && echo hello-fetch > "$WORK/fetchwww/index.html"
ip netns exec $EDGE python3 -m http.server 80 --bind 203.0.113.80 --directory "$WORK/fetchwww" >/dev/null 2>&1 &
sleep 1
ip netns exec $HUB curl -s -m 2 http://fetch.test/ >/dev/null && fail "fetch.test reachable from hub without the tunnel (test setup)"
fetch_running() { api "$base/api/settings/fetch" | grep -q "\"running\":$1"; }
r=$(api -d '{"on":true,"tunnel":"t1","domains":["fetch.test"],"all":true}' "$base/api/settings/fetch")
grep -q '"error"' <<<"$r" && { echo "$r"; fail "turn downloads through the tunnel on"; }
for _ in $(seq 1 10); do fetch_running true && break; sleep 1; done
fetch_running true || { api "$base/api/settings/fetch"; fail "downloads listener not running"; }
ip netns exec $HUB nft list table inet neotunnel_fetch | grep -q "redirect to :7713" || fail "no downloads redirect"
[ "$(ip netns exec $HUB curl -s -m 8 http://fetch.test/)" = hello-fetch ] || { api "$base/api/settings/fetch"; fail "listed site not reached through the tunnel"; }
ip netns exec $HUB curl -s -m 3 http://other.test/ >/dev/null && fail "a site not listed went through the tunnel"
api "$base/api/settings/fetch" | grep -q '"host":"fetch.test","via":"tunnel"' || fail "status does not show fetch.test through the tunnel"
api -d '{"on":false,"tunnel":"t1","domains":["fetch.test"],"all":true}' "$base/api/settings/fetch" | grep -q '"error"' && fail "turn downloads off"
for _ in $(seq 1 10); do fetch_running false && break; sleep 1; done
ip netns exec $HUB nft list table inet neotunnel_fetch 2>/dev/null | grep -q redirect && fail "redirect left after turning downloads off"
ip netns exec $HUB curl -s -m 3 http://fetch.test/ >/dev/null && fail "listed site still went through the tunnel after turning it off"
pass "downloads: a listed site goes through the tunnel and leaves from edge, others stay direct; off removes the redirect"

# 15b4. Backup: one file with the state and the server's key, which the
#       restore page reads back as this same server.
api "$base/api/backup" > "$WORK/backup.json"
grep -q '"format": "neotunnel-backup"' "$WORK/backup.json" && grep -q '"identity.key"' "$WORK/backup.json" || fail "backup lacks the format or the key"
api --data-binary @"$WORK/backup.json" "$base/api/backup/inspect" | tee "$WORK/inspect.json" | grep -q '"node":"hub"' || { cat "$WORK/inspect.json"; fail "backup preview"; }
grep -q '"same_key":true' "$WORK/inspect.json" && grep -q '"edge"' "$WORK/inspect.json" || { cat "$WORK/inspect.json"; fail "backup preview misses the key or the peer"; }
pass "backup: state and key in one file, read back as this server"

# 15c. Optional (NT_BENCH=1): throughput of a TCP forward through a GENEVE
#      tunnel vs a relay forward, on a long lossy path (netem on the link
#      between the servers, both directions).
if [ "${NT_BENCH:-0}" = 1 ]; then
  in_hub tunnel add gv --kind geneve --peer edge >/dev/null || fail "add geneve for the benchmark"
  in_hub forward add gv 5301 --to 5300 --proto tcp >/dev/null || fail "add nat forward over geneve"
  in_hub forward add gv 5302 --to 5300 --mode relay >/dev/null || fail "add relay forward over geneve's pair"
  hup 3
  ip netns exec $EDGE python3 /tcpbench.py server 5300 &
  sleep 1
  NETEM=${NT_NETEM:-delay 50ms loss 0.5%}
  ip netns exec $HUB tc qdisc add dev veth-hub root netem $NETEM
  ip netns exec $EDGE tc qdisc add dev veth-edge root netem $NETEM
  secs=${NT_BENCH_SECS:-20}
  echo "--- netem '$NETEM' each way between the servers, ${secs}s per run, one TCP connection, Mbit/s"
  for cc in cubic bbr; do
    ip netns exec $EDGE sysctl -qw net.ipv4.tcp_congestion_control=$cc
    for dir in down up; do
      nat=$(ip netns exec $CLIENT python3 /tcpbench.py client 198.18.0.1 5301 $dir $secs || echo err)
      rel=$(ip netns exec $CLIENT python3 /tcpbench.py client 198.18.0.1 5302 $dir $secs || echo err)
      echo "BENCH edge-cc=$cc $dir: geneve+nat $nat  relay $rel"
    done
  done
  ip netns exec $HUB tc qdisc del dev veth-hub root
  ip netns exec $EDGE tc qdisc del dev veth-edge root
  ip netns exec $EDGE sysctl -qw net.ipv4.tcp_congestion_control=cubic
  in_hub tunnel remove gv >/dev/null
fi

# 16. Panel: traffic totals, MTU measurement, changing a tunnel's settings.
sleep 11  # one traffic sample
api "$base/api/traffic" | grep -q '"t1"' || fail "no traffic totals for t1"
api "$base/api/metrics?range=1h&keys=cpu,nic,t:t1,p:t1" > "$WORK/metrics.json"
python3 - "$WORK/metrics.json" <<'EOF' || { head -c 600 "$WORK/metrics.json"; fail "graphs history"; }
import json, sys
v = json.load(open(sys.argv[1]))
assert v["step"] == 10 and v["points"] == 360, (v["step"], v["points"])
for k in ("cpu", "t:t1", "p:t1"):
    s = v["series"][k]
    assert any(x is not None for x in s["a"]), k
assert v["series"]["p:t1"]["avg"][0] > 0
EOF
api "$base/api/overview" | python3 -c 'import json,sys; t=[t for t in json.load(sys.stdin)["tunnels"] if t["name"]=="t1"][0]; sys.exit(0 if (t.get("health") or {}).get("probes",0)>=3 else 1)' || fail "overview reports no probe count for t1's health"
pass "graphs history records CPU, tunnel traffic and ping; the overview counts health probes"
api -X POST "$base/api/tunnels/t1/mtu" | grep -q '"path_mtu":1500' || fail "MTU probe"
api -X PATCH -d '{"encrypt":"aes-gcm-256"}' "$base/api/tunnels/e1" | grep -q error && fail "update tunnel settings"
for i in $(seq 1 50); do
  ip netns exec $EDGE ip xfrm state 2>/dev/null | grep -q "rfc4106" && break
  sleep 0.1
done
if [ "$i" -gt 1 ]; then echo "NOTE re-keyed SA on edge appeared after $((i - 1))00 ms"; fi
if ! ip netns exec $EDGE ip xfrm state 2>/dev/null | grep -q "rfc4106"; then
  echo "--- edge xfrm state"; ip netns exec $EDGE ip xfrm state
  echo "--- hub xfrm state"; ip netns exec $HUB ip xfrm state | grep -E "^src|aead" | head
  in_hub peer list
  fail "re-keyed SA missing on edge"
fi
ip netns exec $HUB ping -c 3 -W 2 "$(inner $EDGE nt-e1)" >/dev/null || fail "ping after switching to AES-256"
pass "panel: traffic totals, MTU probe and live settings change"

# 16b. Backup tunnel: port 8083 goes through g1 (plain GRE) with u1 (IPIP in
#      UDP 3001) as its backup. Edge drops 95% of GRE: g1 is still "up" and a
#      ping gets through now and then, which the old one-reply rule took for
#      working. Now the probe rounds grade it bad, the hub restarts it on both
#      servers, and when that doesn't help the forward moves to u1 and keeps
#      working; once GRE passes again for a while it comes back to g1.
mtu_of() { ip -n "$1" -o link show "$2" | grep -o 'mtu [0-9]*' | cut -d' ' -f2; }
wait_for() { # seconds, command...: poll until the command succeeds
  local n=$1; shift
  for _ in $(seq 1 "$n"); do "$@" && return 0; sleep 1; done
  return 1
}
fwd_on() { in_hub forward list | grep -q "8083 .*now through $1"; }
mtu_is() { [ "$(mtu_of "$1" "$2")" = "$3" ]; }
# ov <python expression on the overview, as o>: true when it holds.
ov() { api "$base/api/overview" | python3 -c "import json,sys; o=json.load(sys.stdin); sys.exit(0 if ($1) else 1)"; }
f8083="[f for f in o['forwards'] if f['ports']=='8083'][0]"
in_hub forward add g1 8083 --to 8080 --proto tcp --backup u1 >/dev/null || fail "add forward with a backup"
fwd_on g1 || { in_hub forward list; fail "forward with a backup does not start on its primary"; }
[ "$(http_code http://198.18.0.1:8083/)" = 200 ] || fail "forward 8083 through g1"
ip netns exec $EDGE nft -f - <<'NFT'
table inet e2ecut {
  chain in { type filter hook input priority -300; policy accept; meta l4proto gre numgen random mod 20 != 0 drop; }
}
NFT
t0=$(date +%s)
g1h="[t for t in o['tunnels'] if t['name']=='g1'][0].get('health') or {}"
wait_for 60 ov "($g1h).get('grade') in ('bad', 'down')" || { api "$base/api/overview" | head -c 3000; fail "95% loss on g1 not graded bad"; }
wait_for 60 grep -q "tunnel g1: lost .*restarting it on both servers" "$WORK/hub.log" || { tail -20 "$WORK/hub.log"; fail "g1 at 95% loss was not restarted"; }
echo "NOTE restarted $(( $(date +%s) - t0 )) s after the loss began"
wait_for 90 fwd_on u1 || { in_hub forward list; tail -20 "$WORK/hub.log"; fail "forward did not move to the backup after the restart didn't help"; }
echo "NOTE moved to the backup $(( $(date +%s) - t0 )) s after the loss began"
ov "'restarted' in ($g1h).get('action', '')" || fail "panel does not show the automatic restart"
[ "$(http_code http://198.18.0.1:8083/)" = 200 ] || fail "forward 8083 through the backup u1"
grep -q "forward g1 8083: now through u1" "$WORK/hub.log" || fail "switch to the backup not logged"
ov "$f8083['backup'] == 'u1' and $f8083['active'] == 'u1'" || fail "panel does not show the backup as the active path"
pass "backup: g1 at 95% loss graded bad, restarted once, then port 8083 moved to u1 and kept working"
ip netns exec $EDGE nft delete table inet e2ecut
t0=$(date +%s)
wait_for 150 fwd_on g1 || { in_hub forward list; fail "forward did not come back to its primary"; }
echo "NOTE back on the primary $(( $(date +%s) - t0 )) s after GRE passed again"
[ "$(http_code http://198.18.0.1:8083/)" = 200 ] || fail "forward 8083 back through g1"
ov "$f8083['active'] == 'g1' and [s['to'] for s in $f8083['switches']] == ['u1', 'g1']" || fail "panel switch history"
in_hub forward remove g1 8083 >/dev/null
pass "backup: back on g1 once it was healthy again, with the history in the panel"

# 16c. Safe apply. Kept: the panel and edge confirm, nothing reverts. Cut:
#      after the change the agent port is dropped on edge, so edge can't
#      confirm; the hub reverts at the deadline, and edge, which never heard
#      back, puts its own part back by itself.
g1mtu=$(mtu_of $EDGE nt-g1)
r=$(api -X PATCH -d '{"mtu":1300}' "$base/api/tunnels/g1?safe=1")
grep -q '"safe":{"id":"' <<<"$r" || { echo "$r"; fail "safe change not started"; }
id=$(grep -o '"safe":{"id":"[0-9a-f]*' <<<"$r" | cut -d'"' -f6)
[ "$(mtu_of $EDGE nt-g1)" = 1300 ] || fail "safe change not applied on edge"
[ -f "$WORK/edge/safe-hold.json" ] || fail "edge holds no snapshot of the safe change"
api -X PATCH -d '{"mtu":1280}' "$base/api/tunnels/g1" | grep -q 'منتظر تأیید' || fail "another change accepted while a safe one waits"
api -X POST "$base/api/safe/$id/keep" | grep -q '"kept":true' || fail "keep safe change"
[ -f "$WORK/edge/safe-hold.json" ] && fail "edge still holds the kept change"
[ "$(mtu_of $HUB nt-g1)" = 1300 ] || fail "kept change reverted on hub"
pass "safe apply: confirmed change kept on both servers"

r=$(api -X PATCH -d '{"mtu":1280}' "$base/api/tunnels/g1?safe=1")
id=$(grep -o '"safe":{"id":"[0-9a-f]*' <<<"$r" | cut -d'"' -f6)
[ -n "$id" ] || { echo "$r"; fail "second safe change not started"; }
[ "$(mtu_of $EDGE nt-g1)" = 1280 ] || fail "safe change not applied on edge"
ip netns exec $EDGE nft -f - <<NFT
table inet e2ecut {
  chain in { type filter hook input priority -300; policy accept; tcp dport { 7710, $eport } drop; }
}
NFT
api -X POST "$base/api/safe/$id/keep" >/dev/null
t0=$(date +%s)
wait_for 75 mtu_is $HUB nt-g1 1300 || fail "hub did not revert the unconfirmed change"
echo "NOTE hub reverted $(( $(date +%s) - t0 )) s after the change"
ov "not o['safe_last']['kept'] and o['safe_last']['reason'].startswith('edge did not confirm')" || fail "panel does not say why the change was reverted"
wait_for 30 mtu_is $EDGE nt-g1 1300 || fail "edge did not revert by itself"
grep -q "previous configuration restored" "$WORK/edge.log" || fail "edge did not log its revert"
ip netns exec $EDGE nft delete table inet e2ecut
ip netns exec $HUB ping -c 3 -W 2 "$(inner $EDGE nt-g1)" >/dev/null || fail "ping through g1 after both reverted"
api -X PATCH -d '{}' "$base/api/tunnels/g1" | grep -q error && fail "reset g1 settings"
[ "$(mtu_of $EDGE nt-g1)" = "$g1mtu" ] || fail "g1 MTU not back to automatic"
pass "safe apply: change that cut the agent path reverted on both servers"

# 16c2. Firewall (guard) on hub. The client (198.18.0.2, not an Iranian
#       address) stands in for the internet; edge is a peer. With config
#       ports from Iran only and ping closed: the client gets no ping and
#       no forward, but the panel; the peer's tunnels keep working; a port a
#       program starts listening on is opened within seconds; an allowed
#       address gets the forward; turning it on from the panel without a
#       confirmation turns it off again by itself; off removes the table.
in_hub guard on --iface veth-hub,veth-ci --icmp closed --config-iran >/dev/null || fail "guard on"
ip netns exec $HUB nft list table inet neotunnel_guard >/dev/null || fail "guard table missing"
ip netns exec $CLIENT ping -c 2 -W 1 198.18.0.1 >/dev/null 2>&1 && fail "guard: the client still gets ping"
[ "$(http_code http://198.18.0.1:8080/)" = 200 ] && fail "guard: a config port answers a non-Iranian client"
[ "$(ip netns exec $CLIENT curl -sk -o /dev/null -m 5 -w '%{http_code}' "https://198.18.0.1:$PANEL_PORT/")" = 404 ] || fail "guard: the panel is not reachable"
ip netns exec $HUB ping -c 3 -W 2 "$(inner $EDGE nt-g1)" >/dev/null || fail "guard: tunnel to the peer stopped"
ip netns exec $HUB python3 -m http.server 18555 --directory "$WORK/www" >/dev/null 2>&1 &
newsrv=$!
wait_for 15 sh -c "[ \"\$(ip netns exec $CLIENT curl -s -o /dev/null -m 2 -w '%{http_code}' http://198.18.0.1:18555/)\" = 200 ]" || fail "guard: a new listening port was not opened"
kill $newsrv
api -d '{"on":true,"config_iran":true,"icmp":"closed","allow":["198.18.0.0/24"],"ifaces":["veth-hub","veth-ci"]}' "$base/api/guard" >/dev/null
api -X POST "$base/api/guard/confirm" | grep -q '"ok":true' || fail "guard: confirm"
wait_for 10 sh -c "[ \"\$(ip netns exec $CLIENT curl -s -o /dev/null -m 2 -w '%{http_code}' http://198.18.0.1:8080/)\" = 200 ]" || fail "guard: the allowed client does not get the forward"
in_hub guard status | grep -q "Iran only" || { in_hub guard status; fail "guard status does not list the Iran-only ports"; }
in_hub guard off >/dev/null || fail "guard off"
ip netns exec $HUB nft list table inet neotunnel_guard >/dev/null 2>&1 && fail "guard off left its table"
api -d '{"on":true,"icmp":"closed","ifaces":["veth-hub","veth-ci"]}' "$base/api/guard" | grep -q '"confirm_by":[1-9]' || fail "guard: turned on from the panel without a confirmation deadline"
wait_for 80 sh -c "! ip netns exec $HUB nft list table inet neotunnel_guard >/dev/null 2>&1" || fail "guard: not turned off without a confirmation"
grep -q "firewall turned off again" "$WORK/hub.log" || fail "guard: the automatic turn-off was not logged"
ip netns exec $CLIENT ping -c 2 -W 1 198.18.0.1 >/dev/null || fail "guard: ping still closed after it turned off"
pass "firewall: closes ping and config ports to others, keeps the panel and peers, opens new listeners, turns itself off without a confirmation"

# 16d. A second main server: hub2 manages edge too. Its first tunnel is
#      also called t1 and, before it can know about edge, gets the same inner
#      subnet as hub's t1. edge accepts the second setup key, runs hub2's t1
#      under a local name, and hub2 moves the tunnel to a free subnet once
#      edge reports the clash; hub's t1 is never disturbed.
HUB2=nt-e2e-hub2 HUB2_IP=192.0.3.1 EDGE2_IP=192.0.3.2
ip netns add $HUB2
ip link add veth-h2 netns $HUB2 type veth peer name veth-e2 netns $EDGE
ip -n $HUB2 addr add $HUB2_IP/24 dev veth-h2; ip -n $EDGE addr add $EDGE2_IP/24 dev veth-e2
ip -n $HUB2 link set lo up; ip -n $HUB2 link set veth-h2 up; ip -n $EDGE link set veth-e2 up
in_hub2() { ip netns exec $HUB2 env NEOTUNNEL_DIR=$WORK/hub2 NO_COLOR=1 "$BIN" "$@"; }
in_hub2 node --name hub2 --address $HUB2_IP >/dev/null
ip netns exec $HUB2 env NEOTUNNEL_DIR=$WORK/hub2 "$BIN" daemon >"$WORK/hub2.log" 2>&1 & echo $! > "$WORK/hub2.pid"
sleep 1
in_hub2 peer add edge --address $EDGE2_IP >/dev/null || fail "hub2: add edge"
in_hub2 tunnel add t1 --kind gre --peer edge >/dev/null 2>&1 || true # edge not paired yet
grep -q '"net4": "10.77.0.0/30"' "$WORK/hub2/state.json" || fail "hub2's t1 should start on hub's subnet (it can't know yet)"
key2=$(in_hub2 peer connect edge | grep -o "NT1\.[^']*" | head -1)
out=$(in_edge apply "$key2" 2>&1) || { echo "$out"; fail "edge refused a second main server's setup key"; }
grep -q "managed by 2 main servers" <<<"$out" || { echo "$out"; fail "edge does not report two main servers"; }
in_edge node | grep -q "master.*hub2" || fail "edge does not list hub2 as a master"
ip netns exec $HUB ping -c 2 -W 2 10.77.0.2 >/dev/null || fail "hub's t1 disturbed by the second master"
# hub2 hears edge's report on its next refresh (once a minute) and moves t1.
moved=""
for i in $(seq 90); do
  n=$(ip -n $EDGE -o link show | grep -oE 'nt-t1-[a-z0-9]{3}[@:]' | tr -d '@:' | head -1 || true)
  if [ -n "$n" ] && a=$(inner $EDGE "$n") && [ -n "$a" ] && [ "$a" != "10.77.0.2" ] &&
     ip netns exec $HUB2 ping -c 1 -W 1 "$a" >/dev/null 2>&1; then moved=$n; break; fi
  sleep 1
done
[ -n "$moved" ] || { tail -20 "$WORK/hub2.log"; in_edge tunnel list; fail "hub2's t1 did not come up on edge"; }
ip netns exec $HUB ping -c 2 -W 2 10.77.0.2 >/dev/null || fail "hub's t1 broken after hub2 moved"
in_hub2 peer list | grep -q "edge.*in sync" || fail "hub2 not in sync with edge"
pass "two main servers: edge runs both t1s (hub2's as $moved), hub2 moved its subnet off hub's"
kill "$(cat "$WORK/hub2.pid")" 2>/dev/null; ip netns del $HUB2

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

# 9. Clean removal. The service would recreate the tunnels on its next
#    tick, so it is stopped first, as `down` says.
kill "$(cat "$WORK/hub.pid")" 2>/dev/null; sleep 0.5
in_hub down >/dev/null
ip -n $HUB -o link | grep -qE '^[0-9]+: nt-' && fail "interfaces left after down"
ip netns exec $HUB ip fou show | grep -q "port 3001" && fail "FOU port left after down"
pass "down removes interfaces and FOU ports"

echo
pass "all e2e checks"
