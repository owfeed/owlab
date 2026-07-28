#!/bin/sh
# Radios that LuCI renders, on a box that has no radios.
#
# This works because LuCI's wireless pages are far less kernel-dependent than
# they look:
#
#   * The Network -> Wireless menu appears when the `wifi` system feature is
#     present, and rpcd decides that with access('/sbin/wifi') — i.e. the
#     existence of one file from the wifi-scripts package.
#   * The radio list is pure UCI. network.js getWifiDevices() iterates
#     uci.sections('wireless', 'wifi-device') and merges runtime state as
#     `_state.radios[devname] || {}` — the `|| {}` is what makes a missing phy
#     degrade instead of throw.
#   * SSID, channel, country, BSSID and encryption all fall back to the UCI
#     value when iwinfo has nothing.
#   * The encryption/mode capability matrix is built by running `hostapd
#     -v11ax`, `-vsae` and friends, which report what the BINARY supports and
#     never touch a radio.
#
# So: menu, radio rows, SSIDs, modes, encryption and the full edit form all
# render correctly. What stays zero is signal, noise, bitrate, the channel
# scan and the associated-stations table — those come from libiwinfo, which
# has no dummy backend upstream.
#
# The interfaces deliberately attach to `guest`, never to `lan`. lan is eth0
# with the engine's address on it and the only way into this container; no
# fixture may put anything near it.

# None of the above applies if there are REAL radios.
#
# A fidelity-vm router runs OpenWrt's own kernel, so mac80211_hwsim loads and
# presents genuine phys — hostapd runs on them, iwinfo reports signal and
# noise, and a scan returns results. Overwriting that with the invented
# sections below would be strictly worse than doing nothing: netifd matches a
# wifi-device to a phy by its `path`, the paths here name hardware that is not
# there, and the real radios would sit unclaimed while LuCI drew rows for
# radios that do not exist.
#
# `wifi config` is OpenWrt's own detection — the same code a first boot runs —
# so the result is what the router would have written for itself.
if [ -n "$(ls /sys/class/ieee80211/ 2>/dev/null)" ]; then
	echo "owlab: real radios present, letting wifi config detect them"
	rm -f /etc/config/wireless
	wifi config

	# Detection leaves every interface disabled, which is right for hardware
	# a user has not configured and wrong for a dev box whose entire reason
	# for having radios is to have them up.
	for s in $(uci -q show wireless | sed -n 's/^wireless\.\([^.]*\)\.disabled=.*/\1/p'); do
		uci -q set "wireless.$s.disabled=0"
	done
	for s in $(uci -q show wireless | sed -n "s/^wireless\.\([^.]*\)=wifi-iface$/\1/p"); do
		uci -q set "wireless.$s.ssid=owlab"
		uci -q set "wireless.$s.encryption=psk2"
		uci -q set "wireless.$s.key=owlab123"
		uci -q set "wireless.$s.network=guest"
	done
	# Bands and channels, rather than what detection picked.
	#
	# `wifi config` puts every hwsim radio on 6 GHz with channel auto, because
	# the simulated phy advertises every band and 6 GHz is the highest. That is
	# not what a router looks like: the pages owlab exists to exercise are laid
	# out for a 2.4 GHz radio and a 5 GHz one, and a 6 GHz radio with country
	# '00' comes up on channel 0 with no width the UI can name.
	#
	# A country is not optional either: the default '00' permits almost
	# nothing, and 5 GHz channels come up disabled under it.
	n=0
	for s in $(uci -q show wireless | sed -n "s/^wireless\.\([^.]*\)=wifi-device$/\1/p"); do
		uci -q set "wireless.$s.country=DE"
		if [ "$n" = 0 ]; then
			uci -q set "wireless.$s.band=2g"
			uci -q set "wireless.$s.channel=6"
			uci -q set "wireless.$s.htmode=HT20"
		else
			uci -q set "wireless.$s.band=5g"
			uci -q set "wireless.$s.channel=36"
			uci -q set "wireless.$s.htmode=VHT80"
		fi
		n=$((n + 1))
	done
	uci -q commit wireless
	exit 0
fi

cat > /etc/config/wireless <<'EOF'
config wifi-device 'radio0'
	option type 'mac80211'
	option path 'platform/soc/c000000.wifi'
	option band '2g'
	option channel '6'
	option htmode 'HE20'
	option cell_density '0'
	option disabled '0'

config wifi-iface 'default_radio0'
	option device 'radio0'
	option network 'guest'
	option mode 'ap'
	option ssid 'owlab'
	option encryption 'psk2'
	option key 'owlab123'

config wifi-iface 'guest_radio0'
	option device 'radio0'
	option network 'guest'
	option mode 'ap'
	option ssid 'owlab-guest'
	option encryption 'none'
	option isolate '1'

config wifi-device 'radio1'
	option type 'mac80211'
	option path 'platform/soc/c000000.wifi+1'
	option band '5g'
	option channel '36'
	option htmode 'HE80'
	option cell_density '0'
	option disabled '0'

config wifi-iface 'default_radio1'
	option device 'radio1'
	option network 'guest'
	option mode 'ap'
	option ssid 'owlab-5g'
	option encryption 'sae-mixed'
	option key 'owlab123'

# A disabled interface, because LuCI draws one differently.
config wifi-iface 'iot_radio1'
	option device 'radio1'
	option network 'guest'
	option mode 'ap'
	option ssid 'owlab-iot'
	option encryption 'psk2'
	option key 'owlab123'
	option disabled '1'
EOF

# No `option country` anywhere above, on purpose. A container has no
# regulatory.db, and hostapd on 25.12 refuses to start with country_code 00 —
# which matters the moment someone runs this image at fidelity full, where the
# radios are real.
