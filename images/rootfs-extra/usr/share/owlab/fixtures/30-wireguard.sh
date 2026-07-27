#!/bin/sh
# A WireGuard tunnel with two peers, and the zone and rule it needs.
#
# Peers are what make this worth a fixture: LuCI renders each as its own
# section with its own key fields, and a list of them is a layout nobody sees
# on a bare box. The keys below are throwaway and published — they secure
# nothing and are here only so the fields are not empty.

uci -q batch <<'EOF'
set network.wg0=interface
set network.wg0.proto='wireguard'
set network.wg0.private_key='eIm2XlHrQyPmqmA2wLb6kkFrDzhE3hnA9OuqHIRtR2Q='
set network.wg0.listen_port='51820'
add_list network.wg0.addresses='10.10.0.1/24'

add network wireguard_wg0
set network.@wireguard_wg0[-1].description='phone'
set network.@wireguard_wg0[-1].public_key='xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg='
set network.@wireguard_wg0[-1].allowed_ips='10.10.0.2/32'
set network.@wireguard_wg0[-1].persistent_keepalive='25'

add network wireguard_wg0
set network.@wireguard_wg0[-1].description='laptop'
set network.@wireguard_wg0[-1].public_key='qHMcYlLbLIhkFB1QeS4v7f8SD6L2ktScFSyVw+wr6Uw='
set network.@wireguard_wg0[-1].allowed_ips='10.10.0.3/32'
set network.@wireguard_wg0[-1].route_allowed_ips='1'
commit network
EOF

uci -q batch <<'EOF'
add firewall zone
set firewall.@zone[-1].name='vpn'
set firewall.@zone[-1].input='ACCEPT'
set firewall.@zone[-1].output='ACCEPT'
set firewall.@zone[-1].forward='ACCEPT'
set firewall.@zone[-1].masq='1'
add_list firewall.@zone[-1].network='wg0'

add firewall forwarding
set firewall.@forwarding[-1].src='vpn'
set firewall.@forwarding[-1].dest='lan'

add firewall rule
set firewall.@rule[-1].name='Allow WireGuard'
set firewall.@rule[-1].src='wan'
set firewall.@rule[-1].dest_port='51820'
set firewall.@rule[-1].proto='udp'
set firewall.@rule[-1].target='ACCEPT'
commit firewall
EOF
