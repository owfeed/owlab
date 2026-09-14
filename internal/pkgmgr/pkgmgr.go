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
	"path/filepath"
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

	// Feed is the name AddFeed registered the feed under test with. When set
	// together with Update, the install stops unless that one feed was read.
	//
	// The refresh tolerates a feed that does not answer, and it has to: a
	// distribution sub-index 404s routinely. But the feed under test is the
	// reason the run exists. When IT is the one that did not answer — a wrong
	// URL, an index its key does not verify — the install by name that follows
	// takes a same-named package from the distribution feed, and the test
	// passes against bytes the feed under test never served. Measured on
	// 25.12.4 and 24.10.8 with a probe feed shadowing `tree`: both managers
	// installed the distribution's tree-2.2.1 and exited 0.
	Feed string
}

// Where the feed check looks. Variables rather than literals only so the tests
// can run the generated shell against a temporary directory instead of the
// host's /etc and /var.
var (
	apkReposDir   = "/etc/apk/repositories.d"
	opkgListsDir  = "/var/opkg-lists"
	opkgFeedsConf = "/etc/opkg/customfeeds.conf"
)

// feedCheck is the shell that proves the feed under test was read by the
// refresh it surrounds: before goes ahead of UpdateShell, after behind it.
//
// Each manager gets the one signal that measurement showed cannot lie, and
// neither of them is the refresh's own output.
//
// apk: the bracketed ` [url]` line is printed for a feed that was NOT read
// this time, as long as an earlier run cached its index — a 404 on the second
// run printed the line and `0 unavailable, 1 stale`. What does tell is apk's
// exit code for a refresh of that feed alone, which ignores the cache: rc=0
// for a good feed, rc=1 for a 404, for an index signed by a key the router
// does not trust, and for the stale case (apk-tools 3.0.5, 25.12.4). It costs
// one fetch of one index.
//
// opkg: the list file, removed first. `opkg update` against an index whose
// signature does not verify prints "Updated list of available packages in
// /var/opkg-lists/<name>", then "Signature check failed", deletes the list and
// exits 0 — so neither the marker line nor the exit code says anything. And a
// 404 leaves the list from an earlier run in place, so the file only means
// something if this refresh is what wrote it (24.10.8).
func feedCheck(pm config.PackageManager, name string) (before, after string) {
	refuse := "Refusing to install by name, because " + string(pm) + " would take a same-named package from another feed instead."
	if pm == config.APK {
		return "", "" +
			`owlab_feed=$(cat ` + apkReposDir + `/` + name + `.list 2>/dev/null) || owlab_feed=` + "\n" +
			`owlab_rc=1; owlab_chk="no ` + apkReposDir + `/` + name + `.list"` + "\n" +
			// `x=$(cmd) && a || b`, as in UpdateShell: a failing assignment under
			// `set -e` would end the script before the message below.
			`[ -z "$owlab_feed" ] || { owlab_chk=$(apk update --repositories-file /dev/null -X "$owlab_feed" 2>&1) && owlab_rc=0 || owlab_rc=$?; }` + "\n" +
			`if [ "$owlab_rc" -ne 0 ]; then printf '%s\n' "$owlab_chk" >&2; echo "owlab: the feed under test (` + name + `: $owlab_feed) was not read: apk update --repositories-file /dev/null -X $owlab_feed exited $owlab_rc. ` + refuse + ` Fix --feed (apk wants the URL of packages.adb itself) or --feed-key (the key that signed that index), then rerun." >&2; exit 1; fi` + "\n"
	}
	list := opkgListsDir + "/" + name
	before = `rm -f ` + list + ` ` + list + `.sig || { echo "owlab: cannot remove ` + list + `, so a list from an earlier run would pass for this one" >&2; exit 1; }` + "\n"
	after = "" +
		`if [ ! -s ` + list + ` ]; then ` +
		`owlab_feed=$(sed -n 's/^src\/gz ` + name + ` //p' ` + opkgFeedsConf + ` 2>/dev/null) || owlab_feed=; ` +
		// UpdateShell replays the output only when opkg failed, and a bad
		// signature is exactly the case where it did not.
		`if [ "$owlab_rc" -eq 0 ]; then printf '%s\n' "$owlab_upd" >&2; fi; ` +
		`echo "owlab: the feed under test (` + name + `: $owlab_feed) was not read: opkg update left no ` + list + ` (a 404, or a Packages.sig that --feed-key does not verify, which opkg reports with exit 0). ` + refuse + ` Fix --feed (opkg wants the directory holding Packages.gz) or --feed-key (named by its key id, usign -F -p <key>), then rerun." >&2; exit 1; fi` + "\n"
	return before, after
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
	// Every step stops the script by itself instead of relying on the caller
	// for errexit. `owlab test` and `owlab install` run this through a plain
	// `sh -c` (docker exec) or ssh with no `set -e`, in front of an install BY
	// NAME: a key that did not copy or a repository line that did not write — a
	// full overlay is enough — left the feed missing or untrusted, the tolerant
	// refresh carried on, and `apk add <name>` installed the same-named package
	// from the distribution feed. The test then passed against a package the
	// feed under test never served.
	//
	// `|| { ...; exit 1; }` per line rather than `set -e` at the top, because
	// callers append further commands to this text and a `set -e` would change
	// how every one of those behaves too.
	stop := func(what string) string {
		return ` || { echo "owlab: adding feed ` + name + `: ` + what + ` failed (is the router's overlay full? df /overlay); stopping, because an install by name would fall back to a same-named package from another feed" >&2; exit 1; }` + "\n"
	}
	var b strings.Builder
	if pm == config.APK {
		b.WriteString("mkdir -p /etc/apk/keys /etc/apk/repositories.d" + stop("creating /etc/apk/keys"))
		b.WriteString("cp " + keyFile + " /etc/apk/keys/" + stop("installing its key into /etc/apk/keys"))
		b.WriteString("printf '%s\\n' " + url + " > /etc/apk/repositories.d/" + name + ".list" + stop("writing /etc/apk/repositories.d/"+name+".list"))
		return b.String()
	}
	b.WriteString("mkdir -p /etc/opkg/keys" + stop("creating /etc/opkg/keys"))
	b.WriteString("cp " + keyFile + " /etc/opkg/keys/" + stop("installing its key into /etc/opkg/keys"))
	// An earlier line under the same name goes first. opkg keeps the FIRST
	// `src/gz` of a name and skips the rest ("Duplicate src declaration ...
	// Skipping.", measured on 24.10.8), so a second `owlab install --feed` on a
	// running router with a corrected — or broken — URL kept reading the old
	// one, and the check that the feed under test was read passed on the
	// wrong URL.
	b.WriteString("[ ! -f /etc/opkg/customfeeds.conf ] || sed -i '/^src\\/gz " + name + " /d' /etc/opkg/customfeeds.conf" + stop("removing an earlier "+name+" line from /etc/opkg/customfeeds.conf"))
	// Appended, not written: customfeeds.conf is where a router's own extra
	// feeds live, and replacing it would take them with it.
	b.WriteString("printf 'src/gz %s %s\\n' " + name + " " + url + " >> /etc/opkg/customfeeds.conf" + stop("appending to /etc/opkg/customfeeds.conf"))
	return b.String()
}

// UpdateShell is the shell that refreshes the package index, and the reason
// this package has an opinion about a feed that does not answer.
//
// The text is correct with and without errexit, and has to be: the Dockerfile
// and VM provisioning run it under `set -eu`, while `owlab test` and `owlab
// install` run it through a plain `sh -c`. The one failure that must stop the
// script — no feed answered — stops it with an explicit `exit`.
//
// Both managers exit non-zero when one configured feed is unreachable, and
// under `set -eu` a single dead feed took the whole
// install down before the per-package loop below it ever ran — the loop that
// is deliberately written to tolerate a package this release's feed does not
// carry. The condition is routine rather than exotic: a rootfs image pins its
// kmods index by kernel hash (kmods/6.18.33-1-<hash>/packages.adb), the feed
// keeps only the last handful of kernel builds, and an image older than that
// window 404s on that one sub-index on every run. Retrying does not help; it
// is a 404, not a stall.
//
// What counts as survivable is measured, not reasoned. On
// openwrt/rootfs:x86_64-25.12.4 (2026-09-07), with one unreachable feed added
// beside the eight the image ships:
//
//	ERROR: wget: exited with error 8
//	WARNING: updating and opening .../kmods/6.12.99-1-dead/packages.adb: unexpected end of file
//	 [https://.../targets/x86/64/packages/packages.adb]   <- one per feed that answered, 8 of them
//	1 unavailable, 0 stale; 11279 distinct packages available
//	rc=1
//
// and on the same image with every feed pointed at an unresolvable host:
//
//	8 unavailable, 0 stale; 136 distinct packages available
//	rc=8
//
// So the package count in the summary line is not, on its own, the signal it
// looks like: it counts the installed database too and stays well clear of
// zero when nothing at all was reached. What separates the two runs is whether
// any feed was read, and apk says that in the bracketed lines — eight above,
// none below. Both are required here: a feed was read, and the summary says
// packages are available.
//
// opkg is affected identically (measured on openwrt/rootfs:x86_64-24.10.8 the
// same day: rc=1 with one bad feed among seven good ones, rc=7 with the
// network cut) and marks each feed it read with "Updated list of available
// packages in /var/opkg-lists/<name>". It prints no summary line at all, so on
// that side the count of feeds read is the whole test.
//
// The output is captured rather than silenced, which is the other half of the
// fix: a clean refresh prints nothing, and a partial one replays every line
// the manager wrote, so the log names the feed that did not answer. Tolerating
// a partial refresh silently would leave the developer to guess why a package
// went missing.
//
// images/Dockerfile runs this same shell in the container tier and is held to
// it by TestDockerfileRefreshesTheIndexTheSameWay — Docker cannot call Go, so
// the text is duplicated there, and the two must not drift.
func UpdateShell(pm config.PackageManager) string {
	cmd, marker := "opkg update", "^Updated list of available packages"
	feeds := "/etc/opkg/distfeeds.conf and /etc/opkg/customfeeds.conf"
	// Only apk prints a summary line, so only apk is asked for one.
	summary, dead := "", `[ "$owlab_read" -eq 0 ]`
	partial := `echo "owlab: opkg update: partial refresh, $owlab_read feed(s) read; the feed above did not answer and its packages will be missing" >&2`
	if pm == config.APK {
		cmd, marker = "apk update", `^ \[`
		feeds = "/etc/apk/repositories and /etc/apk/repositories.d"
		summary = `owlab_have=$(printf '%s\n' "$owlab_upd" | sed -n 's/^[0-9][0-9]* unavailable, [0-9][0-9]* stale; \([0-9][0-9]*\) distinct packages available$/\1/p')` + "\n"
		dead = `[ "$owlab_read" -eq 0 ] || [ "${owlab_have:-0}" -eq 0 ]`
		partial = `echo "owlab: apk update: partial refresh, $owlab_read feed(s) read, $owlab_have packages available; the feed above did not answer and its packages will be missing" >&2`
	}
	return "" +
		// `x=$(cmd) && a || b` rather than an if, because a bare failing
		// assignment under `set -e` ends the script right here, which is the
		// bug this function exists to fix.
		`owlab_upd=$(` + cmd + ` 2>&1) && owlab_rc=0 || owlab_rc=$?` + "\n" +
		// grep -c exits 1 when it counts nothing — the very case that has to
		// survive — and still prints the 0.
		`owlab_read=$(printf '%s\n' "$owlab_upd" | grep -c '` + marker + `') || true` + "\n" +
		summary +
		`if [ "$owlab_rc" -ne 0 ]; then printf '%s\n' "$owlab_upd" >&2; fi` + "\n" +
		`if [ "$owlab_rc" -ne 0 ] && { ` + dead + `; }; then echo "owlab: ` + cmd + ` failed: no feed answered. Check the router's network and the feeds in ` + feeds + `" >&2; exit "$owlab_rc"; fi` + "\n" +
		`if [ "$owlab_rc" -ne 0 ]; then ` + partial + `; fi` + "\n"
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
	add := "opkg install"
	if pm == config.APK {
		add = "apk add"
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
		before, after := "", ""
		if o.Feed != "" {
			before, after = feedCheck(pm, o.Feed)
		}
		b.WriteString(before)
		b.WriteString(UpdateShell(pm))
		b.WriteString(after)
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

// Installable reports whether pm can read this package file.
//
// The decision is the file extension and nothing else: a name that is neither
// format — a script, a tarball, an archive a fixture unpacks — is not something
// owlab was asked to judge, and refusing it here would break installs that work.
//
// Only the two package formats are ruled on, because only they are silently
// wrong. apk hands an .ipk to its own parser and reports
// `v2 package format error`, which describes a byte sequence and never mentions
// that this router uses the other manager.
func Installable(pm config.PackageManager, file string) bool {
	switch strings.ToLower(filepath.Ext(file)) {
	case ".apk":
		return pm == config.APK
	case ".ipk":
		return pm == config.OPKG
	}
	return true
}

// SkipNote is the single line naming a file this router was not given.
//
// Said out loud on purpose. A glob that matched only the other release line's
// format leaves nothing to install, and an install of nothing succeeds — so
// without this line the run reports success and then fails every assertion
// after it, with no reason anywhere on screen.
func SkipNote(pm config.PackageManager, file string) string {
	return "owlab: skipping " + filepath.Base(file) + " (this router uses " + string(pm) + ")"
}

// FilterFiles splits package files into the ones pm can install and the ones it
// cannot.
//
// One glob covering both release lines is the natural way to test a package
// that ships in both formats, and it is exactly the layout `owfeed build`
// leaves behind — `dist/noarch/*.apk` for 25.12 beside `dist/all/*.ipk` for
// 24.10. The glob is expanded once on the host, so before this every router got
// every match: the wrong-format file failed, and because all of them go into a
// single command it took the compatible one down with it.
//
// Callers filter before pushing, so no router is handed bytes it has no use for.
func FilterFiles(pm config.PackageManager, files []string) (keep, skipped []string) {
	for _, f := range files {
		if Installable(pm, f) {
			keep = append(keep, f)
			continue
		}
		skipped = append(skipped, f)
	}
	return keep, skipped
}
