#!/usr/bin/env bash
# deploy/cloud-ha/setup.sh's UFW, on the Floating IP's side of a rehearsal node (Epic 50 story
# 50.31, rehearsal T06): from that network only 80, 443 (and 443/udp) and 53 answer, so
# Patroni (8008), Postgres (5432), etcd (2379, 2380) and the loopback api stay unreachable from
# "the internet". The private network is left open, as UFW opens those ports to PRIVATE_NET.
#
# The rules match the destination network, not an interface: fakehcloud moves the Floating IP
# by disconnecting and reconnecting the node, which can rename the interface.
# FIREWALL_HOST is the Docker host (the harness's published ports arrive from it): the
# rehearsal's stand-in for the operator's own access.
set -euo pipefail
net="${FIREWALL_PUBLIC_NET:?the Floating IP network}"
host="${FIREWALL_HOST:?the Docker host on that network}"

iptables -A INPUT -d "${net}" -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
iptables -A INPUT -d "${net}" -s "${host}" -j ACCEPT
for port in 80 443 53; do
	iptables -A INPUT -d "${net}" -p tcp --dport "${port}" -j ACCEPT
done
iptables -A INPUT -d "${net}" -p udp --dport 443 -j ACCEPT
iptables -A INPUT -d "${net}" -p udp --dport 53 -j ACCEPT
iptables -A INPUT -d "${net}" -j DROP
echo "firewall: ${net} answers 80, 443, 53 only"
