#!/bin/sh
# Give the router some clients: static leases, local hostnames, and — at boot
# — fake DHCP leases and ARP neighbours.
#
# This is what turns the empty tables in Status -> Overview and Network ->
# DHCP into tables with rows. An empty table exercises none of the styling a
# populated one does: no zebra striping, no column overflow, no wrapping
# hostnames, no badge alignment.

lan_ip="$(uci -q get network.lan.ipaddr)"
lan_prefix="${lan_ip%.*}"
[ -n "$lan_prefix" ] || lan_prefix="192.168.1"

uci -q batch <<EOF
add dhcp host
set dhcp.@host[-1].name='nas'
set dhcp.@host[-1].mac='b8:27:eb:1a:2b:3c'
set dhcp.@host[-1].ip='$lan_prefix.20'
add dhcp host
set dhcp.@host[-1].name='printer'
set dhcp.@host[-1].mac='00:1b:a9:4f:11:22'
set dhcp.@host[-1].ip='$lan_prefix.21'
add dhcp host
set dhcp.@host[-1].name='tv-lg'
set dhcp.@host[-1].mac='cc:2d:8c:77:88:99'
set dhcp.@host[-1].ip='$lan_prefix.22'
set dhcp.@host[-1].leasetime='infinite'
add dhcp host
set dhcp.@host[-1].name='vacuum'
set dhcp.@host[-1].mac='54:ef:44:0a:bb:cd'
set dhcp.@host[-1].ip='192.168.4.60'
add dhcp host
set dhcp.@host[-1].name='esp-sensor-hall'
set dhcp.@host[-1].mac='a4:cf:12:9e:34:56'
set dhcp.@host[-1].ip='192.168.4.61'

add dhcp domain
set dhcp.@domain[-1].name='nas.lan'
set dhcp.@domain[-1].ip='$lan_prefix.20'
add dhcp domain
set dhcp.@domain[-1].name='grafana.lan'
set dhcp.@domain[-1].ip='$lan_prefix.20'
commit dhcp
EOF

# The ACTIVE leases and neighbours are runtime state, not config: they live in
# /tmp, which procd mounts after uci-defaults run. So this fixture only asks
# for them here and the work happens from rc.local at every boot.
touch /etc/owlab/want-fake-leases
