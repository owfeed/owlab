#!/bin/sh
# Port forwards, including a disabled one.
#
# Needs the `networks` fixture: every redirect here has src='wan', and without
# that zone LuCI draws them with an unresolved source.
#
# The disabled entry is the point of the third one — a disabled row renders
# differently from an enabled one, and that state is easy to leave unstyled.

lan_ip="$(uci -q get network.lan.ipaddr)"
lan_prefix="${lan_ip%.*}"
[ -n "$lan_prefix" ] || lan_prefix="192.168.1"

uci -q batch <<EOF
add firewall redirect
set firewall.@redirect[-1].name='HTTPS to reverse proxy'
set firewall.@redirect[-1].src='wan'
set firewall.@redirect[-1].src_dport='443'
set firewall.@redirect[-1].dest='lan'
set firewall.@redirect[-1].dest_ip='$lan_prefix.20'
set firewall.@redirect[-1].dest_port='443'
set firewall.@redirect[-1].target='DNAT'
set firewall.@redirect[-1].proto='tcp'

add firewall redirect
set firewall.@redirect[-1].name='Torrent'
set firewall.@redirect[-1].src='wan'
set firewall.@redirect[-1].src_dport='51413'
set firewall.@redirect[-1].dest='lan'
set firewall.@redirect[-1].dest_ip='$lan_prefix.20'
set firewall.@redirect[-1].target='DNAT'
set firewall.@redirect[-1].proto='tcp udp'

add firewall redirect
set firewall.@redirect[-1].name='Console (disabled)'
set firewall.@redirect[-1].src='wan'
set firewall.@redirect[-1].src_dport='9000'
set firewall.@redirect[-1].dest='lan'
set firewall.@redirect[-1].dest_ip='$lan_prefix.22'
set firewall.@redirect[-1].target='DNAT'
set firewall.@redirect[-1].proto='tcp'
set firewall.@redirect[-1].enabled='0'
commit firewall
EOF
