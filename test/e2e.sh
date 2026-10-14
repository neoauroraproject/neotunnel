#!/usr/bin/env bash
# End-to-end test on a real kernel: two "servers" (network namespaces) joined
# by a veth link, plus a client namespace. Runs the actual user flow: panel
# setup, quick 6to4, setup link on the peer, automatic pairing and pushes,
# port-based (FOU) tunnels, offline catch-up, tests, bench, port forwarding
# (including a remapped port) and cleanup.
#
# Needs root, iproute2, nftables, curl, python3.  Usage: sudo test/e2e.sh ./neotunnel
set -euo pipefail

BIN=$(realpath "${1:?usage: e2e.sh <neotunnel binary>}")
WORK=$(mktemp -d)
IRAN=nt-e2e-iran KHAREJ=nt-e2e-kharej CLIENT=nt-e2e-client
IRAN_IP=192.0.2.1 KHAREJ_IP=192.0.2.2
PANEL_PORT=34567 PANEL_PASS=e2e-password-123

pass() { printf '\033[32mPASS\033[0m %s\n' "$*"; }
fail() {
  printf '\033[31mFAIL\033[0m %s\n' "$*"
  echo "--- iran daemon log";   tail -n 40 "$WORK/iran.log" 2>/dev/null || true
  echo "--- kharej daemon log"; tail -n 40 "$WORK/kharej.log" 2>/dev/null || true
  exit 1
}
in_iran()   { ip netns exec $IRAN   env NEOTUNNEL_DIR=$WORK/iran   NO_COLOR=1 "$BIN" "$@"; }
in_kharej() { ip netns exec $KHAREJ env NEOTUNNEL_DIR=$WORK/kharej NO_COLOR=1 "$BIN" "$@"; }
start_kharej() { ip netns exec $KHAREJ env NEOTUNNEL_DIR=$WORK/kharej "$BIN" daemon >>"$WORK/kharej.log" 2>&1 & echo $! > "$WORK/kharej.pid"; sleep 1; }
http_code() { ip netns exec $CLIENT curl -s -o /dev/null -m 5 -w '%{http_code}' "$1" || true; }

cleanup() {
  set +e
  for p in $(jobs -p); do kill "$p" 2>/dev/null; done
  [ -f "$WORK/kharej.pid" ] && kill "$(cat "$WORK/kharej.pid")" 2>/dev/null
  for ns in $IRAN $KHAREJ $CLIENT; do ip netns del $ns 2>/dev/null; done
  rm -rf "$WORK"
}
trap cleanup EXIT

for m in sit ipip ip6_tunnel ip_gre ip6_gre vxlan geneve fou; do modprobe $m 2>/dev/null || true; done

ip netns add $IRAN; ip netns add $KHAREJ; ip netns add $CLIENT
ip link add veth-iran netns $IRAN type veth peer name veth-kharej netns $KHAREJ
ip -n $IRAN addr add $IRAN_IP/24 dev veth-iran
ip -n $KHAREJ addr add $KHAREJ_IP/24 dev veth-kharej
ip link add veth-c netns $CLIENT type veth peer name veth-ci netns $IRAN
ip -n $CLIENT addr add 198.18.0.2/24 dev veth-c
ip -n $IRAN addr add 198.18.0.1/24 dev veth-ci
for ns in $IRAN $KHAREJ $CLIENT; do
  ip -n $ns link set lo up
  for dev in $(ip -n $ns -o link show type veth | awk -F': ' '{print $2}' | cut -d@ -f1); do ip -n $ns link set "$dev" up; done
done

# A web server on kharej stands in for the real service behind the tunnel.
mkdir -p "$WORK/www" && echo hello-from-kharej > "$WORK/www/index.html"
ip netns exec $KHAREJ python3 -m http.server 8080 --directory "$WORK/www" >/dev/null 2>&1 &

in_iran node --name iran --address $IRAN_IP >/dev/null
in_kharej node --name kharej --address $KHAREJ_IP >/dev/null
in_iran panel setup --port $PANEL_PORT --password $PANEL_PASS >/dev/null || fail "panel setup"
ip netns exec $IRAN env NEOTUNNEL_DIR=$WORK/iran "$BIN" daemon >"$WORK/iran.log" 2>&1 &
start_kharej
sleep 1

# 1. Quick 6to4 while the peer is not paired: the master prints its commands.
out=$(in_iran quick kharej --address $KHAREJ_IP --forward 8080 --proto tcp)
echo "$out"
link=$(grep -o "neotunnel://[^']*" <<<"$out" | head -1)
[ -n "$link" ] || fail "no setup link printed"
grep -q "curl -fsSk https://$IRAN_IP:7710/dl/" <<<"$out" || fail "no install one-liner"
pass "master printed setup link and install one-liner"

url=$(grep -o "https://$IRAN_IP:7710/dl/[0-9a-f]*" <<<"$out" | head -1)
sum=$(grep -o "echo '[0-9a-f]*" <<<"$out" | head -1 | cut -c7-)
ip netns exec $KHAREJ curl -fsSk "$url" -o "$WORK/dl" || fail "binary download"
echo "$sum  $WORK/dl" | sha256sum -c --quiet || fail "checksum of served binary"
pass "binary served by master with matching checksum"

# 2. The peer applies the link: tunnels come up and it pairs.
in_kharej apply "$link"
in_iran peer list | grep -q "kharej.*in sync" || fail "peer not paired and in sync"
pass "peer paired and in sync"

ip -n $IRAN -d link show nt-t1-6 | grep -q sit || fail "sit missing on iran"
ip -n $KHAREJ -d link show nt-t1 | grep -q ip6tnl || fail "ipip6 missing on kharej"
ip netns exec $IRAN ping -c 3 -W 2 10.77.0.2 >/dev/null || fail "ping through 6to4 (IPv4 over ipip6 over sit)"
pass "6to4 stack up, IPv4 ping through it works"

# 3. Changes on the master reach the peer by themselves.
in_iran tunnel add v1 --kind vxlan --peer kharej | grep -q "kharej updated" || fail "push to peer"
ip -n $KHAREJ -d link show nt-v1 | grep -q vxlan || fail "vxlan not created on peer"
v1peer=$(ip -n $KHAREJ -o -4 addr show dev nt-v1 | awk '{print $4}' | cut -d/ -f1)
ip netns exec $IRAN ping -c 3 -W 2 "$v1peer" >/dev/null || fail "ping through vxlan"
in_iran tunnel remove v1 >/dev/null
ip -n $KHAREJ link show nt-v1 >/dev/null 2>&1 && fail "vxlan still on peer after remove"
pass "add and remove pushed automatically (vxlan)"

# 4. Port-based tunnel: IPIP inside UDP 3001 (FOU), plus a remapped forward.
in_iran tunnel add u1 --kind ipip --peer kharej --port 3001 >/dev/null || fail "add ipip on port 3001"
ip -n $IRAN -d link show nt-u1 | grep -q "encap fou" || fail "nt-u1 is not FOU-encapsulated"
ip netns exec $KHAREJ ip fou show | grep -q "port 3001" || fail "peer has no FOU listener on 3001"
u1peer=$(ip -n $KHAREJ -o -4 addr show dev nt-u1 | awk '{print $4}' | cut -d/ -f1)
ip netns exec $IRAN ping -c 3 -W 2 "$u1peer" >/dev/null || fail "ping through ipip over UDP 3001"
in_iran forward add u1 9090 --to 8080 --proto tcp >/dev/null || fail "add remapped forward"
sleep 16  # one daemon reconcile with the FOU port already open must stay quiet
grep -q "UDP port 3001" "$WORK/iran.log" && fail "daemon reports the open FOU port as a problem"
pass "IPIP on UDP port 3001 works on both ends"

# 5. Offline catch-up: stop the peer, change things, apply one link.
kill "$(cat "$WORK/kharej.pid")"; sleep 0.5
out=$(in_iran tunnel add g1 --kind gre --peer kharej)
grep -q "not reachable" <<<"$out" || fail "expected unreachable peer"
link=$(grep -o "neotunnel://[^']*" <<<"$out" | head -1)
in_kharej apply "$link" >/dev/null
ip -n $KHAREJ -d link show nt-g1 | grep -q gre || fail "gre not applied from offline link"
start_kharej
pass "offline peer caught up with one command"

# 6. Port forwarding from a client: 8080 via 6to4, 9090 -> 8080 via UDP tunnel.
[ "$(http_code http://198.18.0.1:8080/)" = 200 ] || fail "forward 8080 through 6to4"
[ "$(http_code http://198.18.0.1:9090/)" = 200 ] || fail "forward 9090 -> 8080 through ipip/udp"
pass "ports 8080 and 9090(->8080) reach kharej through two different tunnels"

# 7. Built-in test and benchmark.
in_iran test t1 --duration 2s --pings 30 | tee "$WORK/test.txt"
grep -q "Mbit/s\|Gbit/s" "$WORK/test.txt" || fail "test produced no throughput"
pass "tunnel test (ping, jitter, loss, bandwidth)"
in_iran bench kharej --duration 1s --pings 20 | tee "$WORK/bench.txt"
grep -q "fastest tunnel" "$WORK/bench.txt" || fail "bench gave no recommendation"
sleep 1
ip -n $IRAN -o link | grep -qE '^[0-9]+: ntx-' && fail "bench left temporary tunnels on master"
ip -n $KHAREJ -o link | grep -qE '^[0-9]+: ntx-' && fail "bench left temporary tunnels on peer"
pass "benchmark ranked the kinds and cleaned up"

# 8. Panel: login, overview, create a tunnel through the API.
purl=$(in_iran panel info | grep -o "https://[^ ]*")
ppath=$(sed -E 's#https://[^/]+(/[^/]+)/#\1#' <<<"$purl")
base="https://127.0.0.1:$PANEL_PORT$ppath"
jar="$WORK/cookies"
api() { ip netns exec $IRAN curl -sk -b "$jar" -c "$jar" -H 'Content-Type: application/json' -H 'X-NeoTunnel: 1' "$@"; }
[ "$(api -o /dev/null -w '%{http_code}' "$base/api/overview")" = 401 ] || fail "panel API open without login"
api -d "{\"user\":\"admin\",\"password\":\"wrong\"}" "$base/api/login" | grep -q error || fail "panel accepted a wrong password"
api -d "{\"user\":\"admin\",\"password\":\"$PANEL_PASS\"}" "$base/api/login" | grep -q '"ok":true' || fail "panel login"
api "$base/api/overview" | grep -q '"name":"t1"' || fail "overview misses tunnel t1"
api -d '{"peer":"kharej","mode":"gretap","name":"p1"}' "$base/api/tunnels" | grep -q '"ok":true' || fail "create tunnel via panel"
ip -n $KHAREJ -d link show nt-p1 | grep -q gretap || fail "panel-created tunnel missing on peer"
[ "$(ip netns exec $IRAN curl -sk -o /dev/null -w '%{http_code}' "https://127.0.0.1:$PANEL_PORT/")" = 404 ] || fail "panel reachable outside its secret path"
pass "panel: auth, overview and tunnel creation reach the peer"

# 9. Clean removal.
in_iran down >/dev/null
ip -n $IRAN -o link | grep -qE '^[0-9]+: nt-' && fail "interfaces left after down"
ip netns exec $IRAN ip fou show | grep -q "port 3001" && fail "FOU port left after down"
pass "down removes interfaces and FOU ports"

echo
pass "all e2e checks"
