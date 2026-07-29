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

	"owfeed.org/owlab/internal/config"
)

// HostToken is what a feed URL carries in place of an address when the feed is
// being served by the machine running owlab.
//
// There is no literal that is right everywhere, which is why this is a token and
// not documentation telling people what to type. A container reaches the host at
// the bridge gateway, which is 172.17.0.1 on the default bridge of a Linux CI
// runner, something else on a user-defined bridge, and nothing at all under
// Docker Desktop. A VM never sees any of those, because QEMU's user-mode stack
// puts the host at the LAN address owlab hands it. Every pipeline that has tried
// to serve a feed off the runner has hardcoded 172.17.0.1 and then broken on the
// first developer machine.
//
// So the caller writes the token, and owlab substitutes the answer for the tier
// the router actually runs on:
//
//	owlab test --feed 'http://{host}:8080/packages/noarch/packages.adb' --feed-key feed.pem
const HostToken = "{host}"

const (
	// dockerHostAlias resolves through the extra_hosts entry every generated
	// service carries, where it is mapped to Docker's own host-gateway.
	dockerHostAlias = "host.docker.internal"

	// slirpHostAddr is where QEMU's user-mode stack answers for the host on the
	// LAN network owlab configures: net=192.168.1.0/24, host=192.168.1.254.
	// Fixed by owlab, so it is a constant here rather than a lookup.
	slirpHostAddr = "192.168.1.254"
)

// ResolveHost substitutes HostToken for the address this tier reaches the host on.
//
// Left alone when the token is absent, so a URL naming a real feed passes through
// untouched and no caller has to know whether it is dealing with one or the other.
func ResolveHost(url string, vm bool) string {
	if !strings.Contains(url, HostToken) {
		return url
	}
	host := dockerHostAlias
	if vm {
		host = slirpHostAddr
	}
	return strings.ReplaceAll(url, HostToken, host)
}

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

// AddFeed is the shell that points a router at a package feed: the public key
// into the manager's keyring, the repository line into its config.
//
// This is what makes a feed testable end to end. Installing a built file proves
// the package works; installing it by name, out of a signed index, proves the
// thing a subscriber will actually do — and it is the only way to find out that
// an index is unreadable, a URL redirects, or a key does not match before a
// subscriber does.
//
// keyFile is the name the key is written under, and for opkg that name is
// load-bearing: opkg looks a key up by its id, so the file has to be called what
// the feed published it as. apk does not care and matches on the key itself.
//
// The two managers disagree about everything here — apk takes the URL of the
// index FILE, opkg the URL of the DIRECTORY containing it — which is exactly the
// sort of difference that belongs in this package rather than at a call site.
func AddFeed(pm config.PackageManager, name, url, keyFile string) string {
	var b strings.Builder
	if pm == config.APK {
		b.WriteString("mkdir -p /etc/apk/keys /etc/apk/repositories.d\n")
		b.WriteString("cp " + keyFile + " /etc/apk/keys/\n")
		b.WriteString("printf '%s\\n' " + url + " > /etc/apk/repositories.d/" + name + ".list\n")
		return b.String()
	}
	b.WriteString("mkdir -p /etc/opkg/keys\n")
	b.WriteString("cp " + keyFile + " /etc/opkg/keys/\n")
	// Appended, not written: customfeeds.conf is where a router's own extra
	// feeds live, and replacing it would take them with it.
	b.WriteString("printf 'src/gz %s %s\\n' " + name + " " + url + " >> /etc/opkg/customfeeds.conf\n")
	return b.String()
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
