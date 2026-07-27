#!/bin/sh
# LEDs, mounts, swap and cron jobs — the System pages.
#
# None of this does anything. The LED sysfs paths do not exist in a container,
# the UUIDs match no block device, and the cron scripts are not installed. The
# SECTIONS render, which is the entire point: they are forms and tables that
# would otherwise never appear on this box.

uci -q batch <<'EOF'
set system.@system[0].log_size='128'

add system led
set system.@led[-1].name='WAN link'
set system.@led[-1].sysfs='green:wan'
set system.@led[-1].trigger='netdev'
set system.@led[-1].dev='dummy0'
set system.@led[-1].mode='link tx rx'

add system led
set system.@led[-1].name='WLAN 5GHz'
set system.@led[-1].sysfs='green:wlan5'
set system.@led[-1].trigger='phy0radio'
commit system
EOF

uci -q batch <<'EOF'
set fstab.@global[0]=global
set fstab.@global[0].anon_swap='0'
set fstab.@global[0].anon_mount='0'
set fstab.@global[0].auto_swap='1'
set fstab.@global[0].auto_mount='1'
set fstab.@global[0].delay_root='5'
set fstab.@global[0].check_fs='0'

add fstab mount
set fstab.@mount[-1].target='/mnt/usb-storage'
set fstab.@mount[-1].uuid='7f9a1c2e-4b8d-4a1f-9c3e-2d5a6b7c8d9e'
set fstab.@mount[-1].fstype='ext4'
set fstab.@mount[-1].options='rw,noatime'
set fstab.@mount[-1].enabled='1'

add fstab swap
set fstab.@swap[-1].uuid='1a2b3c4d-5e6f-4071-8293-a4b5c6d7e8f9'
set fstab.@swap[-1].enabled='0'
commit fstab
EOF

# System -> Scheduled Tasks renders this file verbatim.
mkdir -p /etc/crontabs
cat > /etc/crontabs/root <<'EOF'
0 4 * * * /usr/libexec/adblock-update.sh >/dev/null 2>&1
*/15 * * * * /usr/bin/vnstat -u >/dev/null 2>&1
30 3 * * 0 /sbin/sysupgrade -b /root/backup-$(date +\%Y\%m\%d).tar.gz
EOF
chmod 600 /etc/crontabs/root
