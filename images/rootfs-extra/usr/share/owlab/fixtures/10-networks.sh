#!/bin/sh
# Invent the networks a real router has: a WAN, a guest bridge, two VLANs, a
# disabled interface, static routes — plus the firewall zones that go with
# them.
#
# Why a dev box needs this at all: LuCI renders almost nothing on its own. The
# sections, tabs, tables, badges and forms a theme or an app exists to style
# only appear when there is CONFIG behind them. A container with one interface
# shows about a fifth of the widget surface, so a whole class of styling bugs
# cannot be seen there. The shapes below are chosen for what they make LuCI
# RENDER — a zone badge, a bridge with ports, a disabled section — not because
# a router would be configured this way.
#
# THE ONE RULE: nothing here may touch `lan`, and no address here may be
# reachable. `lan` is eth0 with the engine's own address and it is the only
# way in; there is no console to recover from. Every network below sits on a
# dummy device or on a VLAN that carries no traffic.

# Derived, not hardcoded: the container's subnet is whatever the engine handed
# out, and it differs between Docker, OrbStack, Colima and Podman.
lan_ip="$(uci -q get network.lan.ipaddr)"
lan_prefix="${lan_ip%.*}"
lan_gw="$(uci -q get network.lan.gateway)"
[ -n "$lan_prefix" ] || lan_prefix="192.168.1"

# Does this router already have a WAN worth keeping?
#
# In a container it does not: the only link is eth0, which is `lan`, so the
# fake WAN below is the only WAN there will ever be. In a VM it does — the
# second NIC is a real interface with a real DHCP lease and a real default
# route, and it is what makes the package manager work. Overwriting it with a
# dummy leaves a router that boots, renders, and cannot reach anything, with
# the cause five uci sections away from the symptom.
#
# The test is whether the configured device EXISTS in the kernel, not whether
# the section exists: a wan section pointing at a device that was never
# created is exactly the state this fixture is meant to fill in.
wan_dev="$(uci -q get network.wan.device)"
have_real_wan=0
if [ -n "$wan_dev" ] && [ -e "/sys/class/net/${wan_dev%%.*}" ]; then
	have_real_wan=1
	echo "owlab: keeping the real wan on $wan_dev"
fi

if [ "$have_real_wan" = 0 ]; then
	uci -q batch <<-EOF
		set network.wan=interface
		set network.wan.device='dummy0'
		set network.wan.proto='static'
		set network.wan.ipaddr='203.0.113.42'
		set network.wan.netmask='255.255.255.0'
		set network.wan.gateway='203.0.113.1'
		set network.wan.dns='9.9.9.9 149.112.112.112'
		set network.wan.peerdns='0'
		# metric 100: this default route must LOSE to lan's. dummy0 discards
		# everything, so a wan route that won would black-hole DNS and the
		# package manager on the dev box itself.
		set network.wan.metric='100'

		# NO ip6gw, and the metric trick that saves the IPv4 side cannot save
		# this one. An ip6gw here would install the ONLY IPv6 default route,
		# pointed at a dummy that discards in silence. Measured on the original
		# of this fixture: 'apk add' then hung in poll() for minutes, because
		# the download server has an AAAA record, happy-eyeballs tried it
		# first, and the packets went nowhere instead of failing fast. Without
		# a v6 default, connect() gets an instant "network unreachable" and
		# falls back to IPv4. The address alone is enough to render the
		# interface with IPv6 on it.
		set network.wan6=interface
		set network.wan6.device='dummy0'
		set network.wan6.proto='static'
		set network.wan6.ip6addr='2001:db8:1a2b:3c4d:5e6f:7a8b:9c0d:1e2f/64'
		set network.wan6.ip6prefix='2001:db8:1a2b:3c4e:5e6f:7a8b:9c0d::/64'
	EOF
fi

# The fake switch ports (veth eth1-eth3, created by the entrypoint). A
# 'config device' is what makes netifd CLAIM them: unclaimed, they carry no
# devtype in 'network.device status', and 'ubus call luci
# getBuiltinEthernetPorts' — the list "Port status" renders — filters on
# exactly that field. They are device sections and not interfaces on purpose,
# so they stay out of Network -> Interfaces and show up only where a port
# belongs.
#
# Claimed only if the device is really there and is not the WAN. A VM has no
# veths — it has one real NIC per -device — and declaring its uplink a switch
# port would take the router's own uplink away from the wan interface.
n=0
for dev in eth1 eth2 eth3; do
	[ -e "/sys/class/net/$dev" ] || continue
	[ "$dev" = "$wan_dev" ] && continue
	n=$((n + 1))
	uci -q set "network.devp$n=device"
	uci -q set "network.devp$n.name=$dev"
done

uci -q batch <<EOF
set network.br_guest=device
set network.br_guest.name='br-guest'
set network.br_guest.type='bridge'
set network.br_guest.ports='dummy1'

set network.guest=interface
set network.guest.device='br-guest'
set network.guest.proto='static'
set network.guest.ipaddr='192.168.3.1'
set network.guest.netmask='255.255.255.0'

set network.vlan_iot=device
set network.vlan_iot.name='eth0.20'
set network.vlan_iot.type='8021q'
set network.vlan_iot.ifname='eth0'
set network.vlan_iot.vid='20'

set network.iot=interface
set network.iot.device='eth0.20'
set network.iot.proto='static'
set network.iot.ipaddr='192.168.4.1'
set network.iot.netmask='255.255.255.0'

set network.vlan_mgmt=device
set network.vlan_mgmt.name='eth0.30'
set network.vlan_mgmt.type='8021q'
set network.vlan_mgmt.ifname='eth0'
set network.vlan_mgmt.vid='30'

set network.mgmt=interface
set network.mgmt.device='eth0.30'
set network.mgmt.proto='static'
set network.mgmt.ipaddr='192.168.9.1'
set network.mgmt.netmask='255.255.255.0'

# A DISABLED interface, because LuCI renders one differently and that state
# needs styling too.
set network.backup=interface
set network.backup.device='dummy1'
set network.backup.proto='dhcp'
set network.backup.disabled='1'

add network route
set network.@route[-1].interface='lan'
set network.@route[-1].target='10.20.0.0'
set network.@route[-1].netmask='255.255.0.0'
set network.@route[-1].gateway='${lan_gw:-$lan_prefix.1}'

commit network
EOF

# A second static route, on the fake WAN only. Pointed at a real uplink its
# gateway would be off-link and netifd would refuse to install it, trading a
# rendered table row for an error in the log.
if [ "$have_real_wan" = 0 ]; then
	uci -q batch <<-EOF
		add network route
		set network.@route[-1].interface='wan'
		set network.@route[-1].target='198.51.100.0'
		set network.@route[-1].netmask='255.255.255.0'
		set network.@route[-1].gateway='203.0.113.1'
		commit network
	EOF
fi

# DHCP pools for the invented networks. These serve for real, and that is
# safe: each sits on a dummy device or on a VLAN nobody is on. `lan` keeps
# ignore=1 (95-owlab-base) because IT is on the bridge shared with the
# engine's other containers.
uci -q batch <<'EOF'
set dhcp.guest=dhcp
set dhcp.guest.interface='guest'
set dhcp.guest.start='100'
set dhcp.guest.limit='150'
set dhcp.guest.leasetime='2h'

set dhcp.iot=dhcp
set dhcp.iot.interface='iot'
set dhcp.iot.start='50'
set dhcp.iot.limit='100'
set dhcp.iot.leasetime='12h'

set dhcp.mgmt=dhcp
set dhcp.mgmt.interface='mgmt'
set dhcp.mgmt.start='10'
set dhcp.mgmt.limit='40'
set dhcp.mgmt.ignore='1'
commit dhcp
EOF

# Make "Port status" a card instead of a single tile.
#
# The port list does NOT come from uci — it comes from /etc/board.json, via
# `ubus call luci getBuiltinEthernetPorts`, which walks the roles there and
# returns one entry per device. The uci `config device` sections above are
# still needed (they make netifd claim the veths, so each reports
# devtype=ethernet), but on their own they add no tiles: an armsr board.json
# names exactly eth0 as lan and eth1 as wan, and that is all the card draws.
#
# One tile exercises none of what that card is — the grid's track sizing, the
# wrap, the linked/unlinked variants, the per-port zone colour. Four do.
#
# eth0 stays in the lan role because that is what it really is. The peer of
# eth3 is left down by the entrypoint, so that tile renders as unlinked.
#
# Not on a VM. There the board.json the image shipped is TRUE — eth0 and eth1
# are the two virtual NICs QEMU created — and replacing it with a card that
# names eth2 and eth3 would draw ports that do not exist.
if [ "$have_real_wan" = 0 ] && [ -s /etc/board.json ] && [ -x /bin/board_detect ]; then
	cat > /etc/board.json <<'EOF'
{
	"model": {
		"id": "owlab,dev",
		"name": "owlab dev router"
	},
	"network": {
		"lan": {
			"ports": [ "eth0", "eth2", "eth3" ],
			"protocol": "static"
		},
		"wan": {
			"device": "eth1",
			"protocol": "dhcp"
		}
	}
}
EOF
fi

# The zones for those networks, so LuCI has more than one badge to draw.
uci -q batch <<'EOF'
add firewall zone
set firewall.@zone[-1].name='guest'
set firewall.@zone[-1].input='REJECT'
set firewall.@zone[-1].output='ACCEPT'
set firewall.@zone[-1].forward='REJECT'
add_list firewall.@zone[-1].network='guest'

add firewall zone
set firewall.@zone[-1].name='iot'
set firewall.@zone[-1].input='REJECT'
set firewall.@zone[-1].output='ACCEPT'
set firewall.@zone[-1].forward='REJECT'
add_list firewall.@zone[-1].network='iot'

add firewall forwarding
set firewall.@forwarding[-1].src='guest'
set firewall.@forwarding[-1].dest='wan'
add firewall forwarding
set firewall.@forwarding[-1].src='iot'
set firewall.@forwarding[-1].dest='wan'

add firewall rule
set firewall.@rule[-1].name='IoT to DNS only'
set firewall.@rule[-1].src='iot'
set firewall.@rule[-1].dest_port='53'
set firewall.@rule[-1].proto='tcp udp'
set firewall.@rule[-1].target='ACCEPT'

add firewall rule
set firewall.@rule[-1].name='Block guest to LAN'
set firewall.@rule[-1].src='guest'
set firewall.@rule[-1].dest='lan'
set firewall.@rule[-1].target='REJECT'
commit firewall
EOF
