package config

// DefaultPackages is what a shipping OpenWrt router has installed, and what a
// router gets here when owlab.yaml does not say otherwise.
//
// The first two groups are copied from OpenWrt's own include/target.mk —
// DEFAULT_PACKAGES and DEFAULT_PACKAGES.router — which is the definition of
// "a default build". The rest is what the firmware selector adds for a
// wireless router with LuCI, checked against the package list of a real
// device (netcore n60 pro, mediatek/filogic).
//
// Most of these are already in an upstream rootfs tarball, so installing them
// is usually a no-op. Naming them anyway is the point: the set is then a
// property of owlab rather than of whichever tarball a distribution happens to
// publish, and a fork with a thinner rootfs gets the same router as OpenWrt.
//
// Anything here can be dropped per project with a `-` entry:
//
//	packages: ["-ppp", "-ppp-mod-pppoe", "+luci-app-sqm"]
//
// and an absolute list (no + or -) replaces the set outright.
func DefaultPackages() []string {
	return []string{
		// --- OpenWrt's DEFAULT_PACKAGES ---
		//
		// libustream-mbedtls is deliberately absent even though it is in the
		// upstream list. The TLS backend is a build-time choice, not a fixed
		// name: every image already carries whichever variant it was built
		// with, and asking ImmortalWrt 24.10 for the mbedtls one fails
		// outright (measured) because that image ships a different provider.
		"base-files",
		"ca-bundle",
		"dropbear",
		"fstools",
		"libc",
		"libgcc",
		"logd",
		"mtd",
		"netifd",
		"uci",
		"uclient-fetch",
		"urandom-seed",
		"urngd",

		// --- DEFAULT_PACKAGES.router ---
		"dnsmasq",
		"firewall4",
		"nftables",
		"kmod-nft-offload",
		"odhcp6c",
		"odhcpd-ipv6only",
		"ppp",
		"ppp-mod-pppoe",

		// --- What makes it a WIRELESS router ---
		//
		// No radio in a container will ever come up, and that is not what
		// these are for. LuCI gates its whole Network -> Wireless menu on
		// access('/sbin/wifi'), which comes from wifi-scripts, and builds the
		// encryption matrix by asking hostapd what it supports — so with
		// these present the pages render, from config, with no radios.
		"wpad-basic-mbedtls",
		"wifi-scripts",

		// --- What the firmware selector adds when you pick LuCI ---
		"luci",
		"luci-app-firewall",
		"luci-app-attendedsysupgrade",
		"luci-app-package-manager",
	}

	// Deliberately NOT included, though a real device's list has them:
	//
	//   fitblk, uboot-envtools, kmod-usb3, kmod-leds-gpio,
	//   kmod-gpio-button-hotplug, kmod-crypto-hw-safexcel,
	//   kmod-mt7915e, kmod-mt7986-firmware, mt7986-wo-firmware
	//
	// Every one of those describes a specific board — its flash layout, its
	// bootloader, its LEDs, its radio silicon. They are properties of the
	// hardware rather than of OpenWrt, none can function without that
	// hardware, and a container has no kernel of its own to load them into.
	// Add them per project if a package under development needs one present
	// on disk to resolve a dependency.
}
