#!/bin/bash
# Inside the container (see run.sh): server on a "public" LAN, phone behind a hard NAT, home behind an
# easy one that only accepts replies. Usage: nat.sh PHONE_NAT_MODE SECS
set -e
nx() { nsenter --net=/run/netns/$1 "${@:2}"; }
mode=$1
ns() { ip netns add $1; nx $1 ip link set lo up; }
for n in inet srv natP phone natH home; do ns $n; done
nx inet ip link add br0 type bridge; nx inet ip link set br0 up
link() { # link NS1 IF1 ADDR1 NS2 IF2 ADDR2
  ip link add $2 netns $1 type veth peer name $5 netns $4
  nx $1 ip addr add $3 dev $2; nx $1 ip link set $2 up
  [ -n "$6" ] && nx $4 ip addr add $6 dev $5; nx $4 ip link set $5 up
}
up() { ip link add $2 netns $1 type veth peer name i$2 netns inet; nx $1 ip addr add $3 dev $2; nx $1 ip link set $2 up; nx inet ip link set i$2 master br0 up; }
up srv e0 10.0.0.1/24; up natP e1 10.0.0.2/24; up natH e2 10.0.0.3/24
link natP lan 192.168.1.1/24 phone eth 192.168.1.2/24; nx phone ip route add default via 192.168.1.1
link natH lan 192.168.2.1/24 home eth 192.168.2.2/24; nx home ip route add default via 192.168.2.1
for n in natP natH; do nx $n sysctl -qw net.ipv4.ip_forward=1; done

# latency: each NAT's link to the internet, both directions
delay() { local j=$(( $3 / 5 )); [ -n "${SEED:-}" ] && j=0; for d in "$1 $2" "inet i$2"; do nx ${d% *} tc qdisc add dev ${d#* } root netem delay ${3}ms ${j}ms; done; }
delay natP e1 ${PHONE_MS:-30}; delay natH e2 ${HOME_MS:-10}

case $mode in random) flag=random span=64512;; block) flag="to :20000-21023 random" span=1024;; preserve) flag="" span=64512;;
  *) echo "phone NAT mode must be random, block or preserve, not $mode"; exit 1;; esac
nx natP nft -f - <<N
table ip nat {
  chain post {
    type nat hook postrouting priority 100;
    oifname "e1" meta l4proto udp masquerade $flag
    oifname "e1" masquerade
  }
}
N
nx natH nft -f - <<N
table ip nat {
  chain post {
    type nat hook postrouting priority 100;
    oifname "e2" masquerade
  }
}
N
# HOME_NAT=cone: home's router lets anyone reach a port it has used (like many home routers once they've
# sent to an IP), instead of only replies from the exact address and port it sent to
[ "${HOME_NAT:-}" = cone ] && nx natH nft -f - <<N
table ip cone {
  chain pre {
    type nat hook prerouting priority -100;
    iifname "e2" udp dport 1024-65535 dnat to 192.168.2.2
  }
}
N
for n in natP natH; do nx $n nft -f - <<N
table inet fw {
  chain input {
    type filter hook input priority 0; policy accept;
    iifname { "e1", "e2" } meta l4proto udp drop
  }
}
N
done

# slow phone: its browser in a cgroup with a CPU cap (processes must leave the root cgroup first)
cg=/sys/fs/cgroup
if [ -n "${PHONE_CPU:-}" ]; then
  mkdir $cg/init $cg/phone
  for p in $(cat $cg/cgroup.procs); do echo $p > $cg/init/cgroup.procs 2>/dev/null || true; done
  echo +cpu > $cg/cgroup.subtree_control
  echo "$(( PHONE_CPU * 1000 )) 100000" > $cg/phone/cpu.max
fi

bin() { [ "$1" = firefox ] && ls /ms-playwright/firefox-*/firefox/firefox || ls /ms-playwright/chromium-*/chrome-linux/chrome; }
printf '#!/bin/sh\n[ -d %s/phone ] && echo $$ > %s/phone/cgroup.procs\nexec nsenter --net=/run/netns/phone %s "$@"\n' $cg $cg $(bin ${PHONE_BR:-chromium}) > /tmp/br-phone
printf '#!/bin/sh\nexec nsenter --net=/run/netns/home %s "$@"\n' $(bin ${HOME_BR:-chromium}) > /tmp/br-home; chmod +x /tmp/br-*
rm -f /out/moz-*
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -subj /CN=10.0.0.1 -keyout /tmp/k.pem -out /tmp/c.pem -days 1 2>/dev/null
nx srv /hi -addr 10.0.0.1:8080 -stun 10.0.0.1:3478 -cert /tmp/c.pem -key /tmp/k.pem -turn-ip 10.0.0.1 > /out/hi.log 2>&1 &
nx natP tcpdump -Z root -U -nni e1 udp -w /tmp/natP.pcap 2>/dev/null &
sleep 1
url=https://10.0.0.1:8080/1234567?diag
NODE_PATH=/pw/node_modules BR_A=${HOME_BR:-chromium} BR_B=${PHONE_BR:-chromium} EXE_A=/tmp/br-home EXE_B=/tmp/br-phone \
  timeout $(( $2 * 2 * (${RELOAD:-0} + 1) + 120 )) node /sim/two.js $url $url $2 || echo "harness exited $?"
sleep 1; pkill -x tcpdump || true; sleep 1
echo "--- public side of the phone's NAT: its mappings toward home, home's probes toward it"
tcpdump -tt -nnr /tmp/natP.pcap 2>/dev/null > /out/all.txt; python3 /sim/pairs.py /out/all.txt $span
[ -n "$TRACE" ] && cp /tmp/natP.pcap /out/nat.pcap # for STUN attributes (stun.py)
