#!/bin/bash
# Throughput and CPU of a user-space tunnel between two network namespaces.
#   go test -c -o dist/tcptun.test ./internal/tcptun   (GOOS=linux; or obfs, eoip)
#   [KIND=tcp|obfs|eoip|eoip-udp] bash tcptun-bench.sh /path/to/x.test [seconds]
# Prints one line per case: Mbit/s and the CPU seconds both ends used per GB.
set -u
BIN=$1
BIN_B=${BIN_B:-$BIN} # far end (another build, for mixed versions)
SECS=${2:-8}
KIND=${KIND:-tcp}
EOIP_PORT=0
# PROF=/dir/name: CPU profiles of both ends ($PROF-a.out, $PROF-b.out); ONE=1: first case only.
PA=${PROF:+-test.cpuprofile=$PROF-a.out} PB=${PROF:+-test.cpuprofile=$PROF-b.out}
[ $KIND = eoip-udp ] && EOIP_PORT=7000
A=ntb-a B=ntb-b
cleanup() {
  pkill -f "[.]test -test.run TestBenchEnd" 2>/dev/null; pkill -f "tcpbench.py server" 2>/dev/null
  ip netns del $A 2>/dev/null; ip netns del $B 2>/dev/null
}
trap cleanup EXIT
cleanup; sleep 0.3
ip netns add $A; ip netns add $B
ip link add va netns $A type veth peer name vb netns $B
ip -n $A addr add 10.99.0.1/24 dev va; ip -n $A link set va up; ip -n $A link set lo up
ip -n $B addr add 10.99.0.2/24 dev vb; ip -n $B link set vb up; ip -n $B link set lo up
for ns in $A $B; do
  ip netns exec $ns sysctl -qw net.core.rmem_max=33554432 net.core.wmem_max=33554432 \
    net.ipv4.tcp_rmem="4096 131072 33554432" net.ipv4.tcp_wmem="4096 65536 33554432"
done
ip netns exec $B python3 /tcpbench.py server 5201 &

cpu() { # utime+stime ticks of a pid
  awk '{print $14+$15}' /proc/$1/stat 2>/dev/null || echo 0
}

run() { # label path streams flows cipher dir
  local label=$1 path=$2 streams=$3 flows=$4 cipher=$5 dir=$6
  case $path in
    clean) ip netns exec $A tc qdisc del dev va root 2>/dev/null; ip netns exec $B tc qdisc del dev vb root 2>/dev/null ;;
    lossy) ip netns exec $A tc qdisc replace dev va root netem delay 25ms loss 0.25% limit 100000
           ip netns exec $B tc qdisc replace dev vb root netem delay 25ms loss 0.25% limit 100000 ;;
  esac
  ip netns exec $A env NT_BENCH_SIDE=0 NT_BENCH_DEV=ntb0 NT_BENCH_PORT=7000 NT_BENCH_REMOTE=10.99.0.2:7000 \
    NT_BENCH_EOIP_PORT=$EOIP_PORT NT_BENCH_STREAMS=$streams NT_BENCH_CIPHER=$cipher $BIN $PA -test.run TestBenchEnd -test.v >/tmp/ea.log 2>&1 &
  local pa=$!
  ip netns exec $B env NT_BENCH_SIDE=1 NT_BENCH_DEV=ntb0 NT_BENCH_PORT=7000 NT_BENCH_REMOTE=10.99.0.1:7000 \
    NT_BENCH_EOIP_PORT=$EOIP_PORT NT_BENCH_STREAMS=$streams NT_BENCH_CIPHER=$cipher $BIN_B $PB -test.run TestBenchEnd -test.v >/tmp/eb.log 2>&1 &
  local pb=$!
  for i in $(seq 50); do
    ip -n $A link show ntb0 >/dev/null 2>&1 && ip -n $B link show ntb0 >/dev/null 2>&1 && break; sleep 0.1
  done
  ip -n $A addr add 172.31.99.1/30 dev ntb0; ip -n $A link set ntb0 up mtu 1400
  ip -n $B addr add 172.31.99.2/30 dev ntb0; ip -n $B link set ntb0 up mtu 1400
  for i in $(seq 100); do ip netns exec $A ping -c1 -W1 172.31.99.2 >/dev/null 2>&1 && break; sleep 0.1; done
  sleep 1 # let every stream connect
  # ip netns exec and env both exec, so $pa and $pb are the test binaries.
  local ta=$(cpu $pa) tb=$(cpu $pb)
  local outs=() f
  for f in $(seq $flows); do
    ip netns exec $A python3 /tcpbench.py client 172.31.99.2 5201 $dir $SECS >/tmp/f$f.out 2>&1 &
    outs+=($!)
  done
  wait "${outs[@]}"
  local ta2=$(cpu $pa) tb2=$(cpu $pb)
  local mbit=$(cat /tmp/f*.out | awk '{s+=$1} END {printf "%.0f", s}')
  rm -f /tmp/f*.out
  kill $pa $pb 2>/dev/null; wait $pa $pb 2>/dev/null
  local cpus=$(( (ta2 - ta) + (tb2 - tb) ))
  awk -v l="$label" -v m="$mbit" -v c="$cpus" -v s="$SECS" 'BEGIN {
    gb = m * s / 8 / 1000; printf "%-34s %6d Mbit/s   cpu %5.2f s (both ends)   %5.2f cpu-s/GB\n", l, m, c/100, (gb > 0 ? c/100/gb : 0) }'
  sleep 0.5
}

if [ $KIND != tcp ]; then
  run "$KIND clean flows=1 up"   clean 1 1 "" up
  [ -n "${ONE:-}" ] && exit
  run "$KIND clean flows=4 up"   clean 1 4 "" up
  run "$KIND clean flows=4 down" clean 1 4 "" down
  run "$KIND lossy flows=4 up"   lossy 1 4 "" up
  exit
fi
for c in "" aes-gcm-128; do
  n=${c:-plain}
  run "clean  streams=1 flows=1 $n up"   clean 1 1 "$c" up
  run "clean  streams=4 flows=4 $n up"   clean 4 4 "$c" up
  run "clean  streams=4 flows=4 $n down" clean 4 4 "$c" down
done
run "lossy  streams=1 flows=4 plain up" lossy 1 4 "" up
run "lossy  streams=4 flows=4 plain up" lossy 4 4 "" up
run "lossy  streams=8 flows=8 plain up" lossy 8 8 "" up
