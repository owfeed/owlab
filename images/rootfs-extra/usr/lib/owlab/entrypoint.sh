#!/bin/sh
# Hand netifd the address Docker has ALREADY put on eth0 — before procd boots.
#
# THE TIMING IS THE WHOLE POINT, and it is why this is not a uci-defaults
# script. Two things happen during OpenWrt's boot that a container does not
# survive by default:
#
#   1. By the time /etc/uci-defaults/* run, eth0 is down and flushed. A
#      uci-default cannot read the address Docker assigned, because there is
#      nothing left to read. Anything that derives config from the live
#      interface has to run out here, before `exec /sbin/init`.
#
#   2. /bin/config_generate then writes its own config from /etc/board.json,
#      which on x86 and armsr means br-lan with eth0 enslaved and a hardcoded
#      192.168.1.1. netifd applies it, Docker's address is gone, and the
#      container is unreachable with no console to fix it from.
#
# config_generate's guard is `[ -s /etc/config/network -a -s /etc/config/system ]
# && exit 0`, so writing BOTH files here is what disarms it. That is why
# /etc/config/system ships in the image even though nothing in it is
# interesting: an empty or missing one re-arms the generator.
#
# The values are derived from the interface rather than passed in from
# compose, because a copy in compose is the copy nobody remembers to change.
# And because they match what is already on the wire, netifd re-asserting
# them is a no-op.
set -e

# Wait for the address rather than sampling once.
#
# Docker starts the container's process and configures its interface
# concurrently, so eth0 can still be bare when PID 1 begins — reliably so on
# a restart, on a loaded machine, and on the slower desktop engines. Sampling
# once and finding nothing is not harmless: we then skip writing
# /etc/config/network, config_generate fires, and the box comes up on
# br-lan/192.168.1.1 with no route to it and no console to fix it from. The
# symptom is a container that runs and answers nothing.
addr=""
gw=""
i=0
while [ "$i" -lt 100 ]; do
	addr="$(ip -4 -o addr show dev eth0 2>/dev/null | awk '{print $4; exit}')"
	[ -n "$addr" ] && break
	i=$((i + 1))
	# busybox sleep takes fractions; 0.1 x 100 is ten seconds, far longer
	# than this has ever taken and still short enough not to look hung.
	sleep 0.1
done
gw="$(ip -4 route show default 2>/dev/null | awk '{print $3; exit}')"

# DNS: keep whatever the engine gave us, and append public resolvers behind
# it. Neither half is optional, because the engines disagree about which one
# works.
#
#   * Docker Desktop and WSL2 hand out the embedded resolver 127.0.0.11,
#     which is implemented with NAT rules that OpenWrt's own boot flushes. It
#     stops answering partway through the boot, so something else has to be
#     in the list.
#   * OrbStack hands out 0.250.250.200, which keeps working after boot — and
#     there, direct UDP/53 to the internet is not forwarded at all, so the
#     public resolvers alone resolve nothing. Overwriting the file the way
#     the Docker Desktop case wants leaves the container with no DNS.
#
# Reading the file before overwriting it is what makes one image work on
# both. resolv.conf is a plain file here (Docker wrote it, so OpenWrt's usual
# symlink into /tmp is not in play) and the entrypoint owns it from now on;
# netifd's own /tmp/resolv.conf.d/resolv.conf.auto is not consulted.
engine_ns="$(awk '$1 == "nameserver" { print $2 }' /etc/resolv.conf 2>/dev/null | tr '\n' ' ')"
fallback_ns="${OWLAB_DNS:-1.1.1.1 8.8.8.8}"
dns="$(printf '%s %s\n' "$engine_ns" "$fallback_ns" | tr ' ' '\n' | awk 'NF && !seen[$0]++' | tr '\n' ' ')"
{
	for ns in $dns; do echo "nameserver $ns"; done
	# So that a resolver which died mid-boot costs one second, not five.
	echo "options timeout:1"
} > /etc/resolv.conf

# lan is a BRIDGE named br-lan, with eth0 as its only port.
#
# Not decoration. br-lan is what every OpenWrt target with more than one
# ethernet port actually builds, so packages and scripts refer to it by name:
# an interface list in a package's own config defaults to br-lan, and so do plenty of
# firewall snippets, hotplug scripts and forum recipes. On a router whose lan
# is a bare eth0 all of those silently apply to nothing — no error, just a
# feature that does not work.
#
# The bridge takes eth0's MAC (lowest port MAC), so the engine's veth peer
# keeps delivering to the same address it always did and nothing on the host
# side notices. eth0's own address and its connected route are removed before
# netifd starts, because a bridge port cannot carry L3: leaving them there
# gives the kernel a route out of an interface that no longer transmits.
#
# OWLAB_LAN_BRIDGE=0 falls back to lan on eth0 directly. This is the one
# interface that must not break, and an engine that dislikes bridging its veth
# should be one variable away from working, not one image rebuild.
lan_dev="br-lan"
[ "${OWLAB_LAN_BRIDGE:-1}" = "1" ] || lan_dev="eth0"

if [ -n "$addr" ]; then
	netmask="$(ipcalc.sh "$addr" | sed -n 's/^NETMASK=//p')"
	bridge_section=""
	if [ "$lan_dev" = "br-lan" ]; then
		bridge_section="config device
	option name 'br-lan'
	option type 'bridge'
	list ports 'eth0'
"
	fi
	cat > /etc/config/network <<-EOF
		config interface 'loopback'
			option device 'lo'
			option proto 'static'
			option ipaddr '127.0.0.1'
			option netmask '255.0.0.0'

		config globals 'globals'

		$bridge_section
		config interface 'lan'
			option device '$lan_dev'
			option proto 'static'
			option ipaddr '${addr%/*}'
			option netmask '$netmask'
			option gateway '$gw'
			option dns '$dns'
			option delegate '0'
	EOF

	# Hand the address over to netifd rather than leaving two claims on it.
	# Done last, so that everything above has already been written: from here
	# until netifd brings br-lan up the container has no address at all, and
	# an error in between would be unrecoverable.
	if [ "$lan_dev" = "br-lan" ]; then
		ip addr flush dev eth0 2>/dev/null
	fi
else
	# Say plainly what is about to happen, because the alternative is a
	# container that boots cleanly and is simply unreachable.
	echo "owlab: eth0 still has no IPv4 after 10s." >&2
	echo "owlab: config_generate will now invent br-lan/192.168.1.1 and this container" >&2
	echo "owlab: will NOT be reachable on its published ports. Check the engine's networking." >&2
fi

# Rewritten on every start, not just the first: a container is recreated far
# more often than a router reboots, and a stale address here is the one
# failure that locks us out.
sed -i "s/^\toption hostname .*/\toption hostname '$(cat /proc/sys/kernel/hostname)'/" /etc/config/system

# Everything from here to the ssh key is COSMETIC — invented interfaces that
# make LuCI's pages look like a router instead of a container. None of it may
# stop the boot, so the whole block runs with errexit off. Without that, one
# unsupported `ip` operation turns into a container that restarts forever, and
# the log says only "SIOCGIFFLAGS: No such device".
set +e

# Fixture networks hang off these. netifd knows how to make a bridge or a
# VLAN but not a dummy, so a dummy has to exist before it looks — which means
# here, before procd starts. They carry no traffic: a dummy discards, and that
# is exactly why a fake WAN can have an address and a gateway without
# endangering the one interface that is real.
for d in dummy0 dummy1; do
	ip link show "$d" >/dev/null 2>&1 || ip link add "$d" type dummy 2>/dev/null
done

# Fake switch ports, so "Port status" is a card and not a single tile. x86 and
# armsr have one port, and one tile exercises none of what that card is: the
# grid's track sizing, the wrap, the linked/unlinked variants, the per-port
# zone colour.
#
# Three constraints decide the shape below, and each was measured on a box:
#
#   * veth, NOT dummy. The port list comes from
#     `ubus call luci getBuiltinEthernetPorts`, which keeps a device only if
#     netifd reports devtype ethernet or dsa — and netifd reports no devtype
#     at all for a dummy, so a dummy is invisible to that card however it is
#     named or configured.
#   * named ethN, because the same filter also wants link-advertising OR a
#     name matching ^eth\d+$, and a veth advertises nothing.
#   * one pair's peer is left down: carrier follows the peer, so that pair is
#     the "no link" tile beside the linked ones.
#
# The peer name is READ BACK rather than chosen. OpenWrt's `ip` is busybox's,
# and busybox silently ignores `peer name <x>` — it creates the pair and names
# the peer vethN. Asking for eth1p and then bringing up eth1p is how this
# looked like a working script while failing on every boot.
#
# They carry no network and appear in no uci config — the card reads the
# device list, not the config.
if [ "${OWLAB_FAKE_PORTS:-1}" = "1" ]; then
	for n in 1 2 3; do
		if ! ip link show "eth$n" >/dev/null 2>&1; then
			ip link add "eth$n" type veth peer name "eth${n}p" 2>/dev/null || continue
		fi
		ip link set "eth$n" up 2>/dev/null

		# "eth1@veth0" -> "veth0"; busybox prints the peer after an @.
		peer="$(ip -o link show "eth$n" 2>/dev/null | awk -F'[@:]' '{print $3; exit}' | tr -d ' ')"
		[ -n "$peer" ] || continue
		# Leave the last one's peer down, so one tile shows as unlinked.
		[ "$n" = 3 ] || ip link set "$peer" up 2>/dev/null
	done
fi

# compose bind-mounts the host's public key to a staging path, because a bind
# mount carries the host's ownership and dropbear rejects an authorized_keys
# it does not see as root's — reporting only "Permission denied (publickey)",
# which reads like a wrong key. Copying it gives root:root 0600 and nothing to
# debug.
#
# cp+chown+chmod rather than `install`: OpenWrt's busybox has no install
# applet, and `set -e` would turn that into a boot loop.
if [ -s /etc/owlab/authorized_keys.host ]; then
	mkdir -p /etc/dropbear
	cp /etc/owlab/authorized_keys.host /etc/dropbear/authorized_keys
	chown root:root /etc/dropbear/authorized_keys
	chmod 0600 /etc/dropbear/authorized_keys
fi

exec /sbin/init
