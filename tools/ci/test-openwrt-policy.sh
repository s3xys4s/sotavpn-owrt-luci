#!/bin/bash
# Exercise the installed classifier with packets from a separate LAN namespace.
set -euo pipefail
root=$1
[[ $$ == 1 ]]
mount -t proc proc "$root/proc"
ip link set lo up
unshare --net sleep 180 &
client=$!
trap 'kill "$client" 2>/dev/null || :' EXIT
for _ in {1..50}; do
    [[ $(readlink "/proc/$client/ns/net") != "$(readlink /proc/self/ns/net)" ]] && break
    sleep .1
done
lan=(nsenter -t "$client" -n)
ip link add lan0 type veth peer name eth0 netns "$client"
ip addr add 192.0.2.1/24 dev lan0
ip link set lan0 up
"${lan[@]}" ip link set eth0 up
"${lan[@]}" ip addr add 192.0.2.2/24 dev eth0
"${lan[@]}" ip route add default via 192.0.2.1
chroot "$root" /bin/sh -ec '
    mkdir -p /var/run/sota /etc/sota
    uci -q delete sota.main.rule_hash || :
    uci set sota.main.rule_format=2
    uci set sota.main.enabled=1
    uci set sota.main.protected_device=lan0
    uci commit sota'
echo '{"manual":"198.51.100.0/24","exceptions":{"manual":"198.51.100.20/32"}}' > "$root/etc/sota/route-list.txt"
nft -f - <<'RULES'
table inet observe {
    counter vpn {}
    counter wan {}
    chain ingress {
        type filter hook prerouting priority -154; policy accept;
        iifname "lan0" udp dport 43191 meta mark 0x534f counter name vpn
        iifname "lan0" udp dport 43191 meta mark 0 counter name wan
    }
}
RULES
packet() {
    local addr=$1 expected=$2
    nft reset counters table inet observe >/dev/null
    "${lan[@]}" python3 -c 'import socket,sys; socket.socket(type=socket.SOCK_DGRAM).sendto(b"test",(sys.argv[1],43191))' "$addr"
    for _ in {1..20}; do
        if nft list counter inet observe "$expected" | grep -q 'packets 1 '; then return 0; fi
        sleep .1
    done
    echo "Expected $addr via $expected" >&2
    nft list table inet observe >&2
    return 1
}
for direction in vpn wan; do
    chroot "$root" /sbin/uci set "sota.main.list_direction=$direction"
    chroot "$root" /usr/libexec/sota-policy apply
    chroot "$root" /usr/libexec/sota-policy check
    packet 198.51.100.10 "$direction"
    packet 198.51.100.20 wan
    if [[ $direction == vpn ]]; then packet 203.0.113.30 wan; else packet 203.0.113.30 vpn; fi
done
chroot "$root" /usr/libexec/sota-policy clear
if nft list table inet sota_guard >/dev/null 2>&1 || ip -4 rule show | grep -q 'lookup 21519'; then exit 1; fi
packet 203.0.113.30 wan
echo 'PASS real LAN packets, both routing directions, CIDR exception priority and policy cleanup'
