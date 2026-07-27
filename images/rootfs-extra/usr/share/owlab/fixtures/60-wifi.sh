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
