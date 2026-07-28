// Package pkgmgr builds the shell that installs packages on a router.
//
// A router's own package manager is the only correct one to use, and which one
// that is depends on the release: OpenWrt switched from opkg to apk in 25.12,
// and a fork can track 25.12 while still building with opkg. Both tiers hit
// this — the container over `docker exec`, the VM over ssh — and both install
// the same three kinds of thing (feed packages, local files, out-of-feed
// downloads). Writing the commands here rather than at each call site is what
// keeps the two tiers installing identically.
package pkgmgr

import (
	"strings"

	"github.com/VizzleTF/owlab/internal/config"
)

// Options are the differences between one install and another.
type Options struct {
	// Update refreshes the package index first. Wanted for anything named by
	// feed, pointless for a file already on the router.
	Update bool

	// Untrusted accepts a package carrying no signature the router's keyring
	// knows, which is every locally built or downloaded one. apk refuses them
	// outright without it; opkg has no equivalent and ignores this.
	Untrusted bool

	// Overwrite lets a package take a file another package already owns.
	//
	// Needed by out-of-feed packages that pull a replacement dependency:
	// luci-app-openclash needs dnsmasq-full, which collides with the dnsmasq
	// already in the image over /etc/init.d/dnsmasq.
	Overwrite bool

	// Tolerant installs one package at a time and reports the ones this
	// release's feed does not have, instead of failing the lot.
	//
	// Feeds differ between releases and between a fork and upstream, so a name
	// missing on 24.10 should cost that package and not the whole router. The
	// skipped names are printed, because silently getting fewer packages than
	// were asked for is the failure this is meant to make visible.
	Tolerant bool
}

// Install is the shell that installs pkgs, or "true" when there is nothing to
// install — a script fragment that is always safe to concatenate.
//
// pkgs may name feed packages or absolute paths to files already pushed onto
// the router; both managers accept either in the same position.
func Install(pm config.PackageManager, pkgs []string, o Options) string {
	if len(pkgs) == 0 {
		return "true"
	}
	add, update := "opkg install", "opkg update"
	if pm == config.APK {
		add, update = "apk add", "apk update"
	}

	var flags []string
	if o.Untrusted && pm == config.APK {
		flags = append(flags, "--allow-untrusted")
	}
	if o.Overwrite {
		flags = append(flags, "--force-overwrite")
	}
	if len(flags) > 0 {
		add += " " + strings.Join(flags, " ")
	}

	var b strings.Builder
	if o.Update {
		// Silenced on both streams: an index refresh reports mirrors and
		// signature checks that say nothing about the install that follows.
		b.WriteString(update + " >/dev/null 2>&1\n")
	}
	if o.Tolerant {
		b.WriteString("for p in " + strings.Join(pkgs, " ") + "; do\n")
		b.WriteString("  " + add + " \"$p\" >/dev/null 2>&1 || echo \"owlab: skip (not in this feed): $p\"\n")
		b.WriteString("done\n")
		return b.String()
	}
	b.WriteString(add + " " + strings.Join(pkgs, " ") + "\n")
	return b.String()
}
