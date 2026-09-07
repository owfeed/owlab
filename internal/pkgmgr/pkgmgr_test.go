package pkgmgr

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	owlab "owfeed.org/owlab"
	"owfeed.org/owlab/internal/config"
)

func TestInstallPicksTheRoutersOwnManager(t *testing.T) {
	apk := Install(config.APK, []string{"luci"}, Options{Update: true})
	if !strings.Contains(apk, "apk update") || !strings.Contains(apk, "apk add luci") {
		t.Errorf("apk install:\n%s", apk)
	}
	if strings.Contains(apk, "opkg") {
		t.Errorf("apk install mentions opkg:\n%s", apk)
	}

	opkg := Install(config.OPKG, []string{"luci"}, Options{Update: true})
	if !strings.Contains(opkg, "opkg update") || !strings.Contains(opkg, "opkg install luci") {
		t.Errorf("opkg install:\n%s", opkg)
	}
}

// Nothing to install has to produce a script fragment that is safe to
// concatenate, because callers glue this into a larger shell script.
func TestInstallOfNothingIsTrue(t *testing.T) {
	if got := Install(config.APK, nil, Options{Update: true}); got != "true" {
		t.Errorf("Install(nil) = %q, want \"true\"", got)
	}
}

// --allow-untrusted is apk's alone; opkg has no equivalent and would reject
// the flag outright.
func TestUntrustedIsAPKOnly(t *testing.T) {
	apk := Install(config.APK, []string{"/tmp/x.apk"}, Options{Untrusted: true})
	if !strings.Contains(apk, "--allow-untrusted") {
		t.Errorf("apk needs --allow-untrusted for an unsigned file:\n%s", apk)
	}
	opkg := Install(config.OPKG, []string{"/tmp/x.ipk"}, Options{Untrusted: true})
	if strings.Contains(opkg, "--allow-untrusted") {
		t.Errorf("opkg has no --allow-untrusted:\n%s", opkg)
	}
}

func TestOverwriteAppliesToBoth(t *testing.T) {
	for _, pm := range []config.PackageManager{config.APK, config.OPKG} {
		got := Install(pm, []string{"/tmp/x"}, Options{Overwrite: true})
		if !strings.Contains(got, "--force-overwrite") {
			t.Errorf("%s:\n%s", pm, got)
		}
	}
}

// A file already on the router needs no index refresh, and asking for one
// would make an offline install fail on a network it does not need.
func TestNoUpdateMeansNoUpdate(t *testing.T) {
	got := Install(config.APK, []string{"/tmp/x.apk"}, Options{Untrusted: true})
	if strings.Contains(got, "apk update") {
		t.Errorf("refreshed the index for a local file:\n%s", got)
	}
}

// Tolerant is what keeps one name missing on 24.10 from costing the whole
// router: each package is tried alone and the skips are reported.
func TestTolerantInstallsOneAtATimeAndReportsSkips(t *testing.T) {
	got := Install(config.OPKG, []string{"luci", "luci-app-sqm"}, Options{Update: true, Tolerant: true})
	if !strings.Contains(got, "for p in luci luci-app-sqm; do") {
		t.Errorf("not a per-package loop:\n%s", got)
	}
	if !strings.Contains(got, `opkg install "$p"`) {
		t.Errorf("loop does not install the loop variable:\n%s", got)
	}
	if !strings.Contains(got, "owlab: skip (not in this feed): $p") {
		t.Errorf("a skipped package is not reported:\n%s", got)
	}
	if !strings.Contains(got, "||") {
		t.Errorf("a failed package would abort the loop:\n%s", got)
	}
}

// The two managers disagree about the shape of a feed, and the difference is not
// cosmetic: apk is pointed at the index FILE, opkg at the DIRECTORY holding it.
// Swap them and the router fetches something that is not an index.
func TestAddFeedSpeaksEachManagersDialect(t *testing.T) {
	apk := AddFeed(config.APK, "demo", "'https://example.org/releases/25.12/x86_64/packages.adb'", "/tmp/demo.pem")
	if !strings.Contains(apk, "/etc/apk/keys/") {
		t.Errorf("apk feed does not install the key:\n%s", apk)
	}
	if !strings.Contains(apk, "/etc/apk/repositories.d/demo.list") {
		t.Errorf("apk feed does not write a repository line:\n%s", apk)
	}
	if strings.Contains(apk, "src/gz") {
		t.Errorf("apk feed uses opkg syntax:\n%s", apk)
	}

	opkg := AddFeed(config.OPKG, "demo", "'https://example.org/releases/24.10/x86_64'", "/tmp/9040356b214084da")
	if !strings.Contains(opkg, "src/gz") {
		t.Errorf("opkg feed does not write a src/gz line:\n%s", opkg)
	}
	if !strings.Contains(opkg, ">> /etc/opkg/customfeeds.conf") {
		t.Errorf("opkg feed replaces customfeeds.conf instead of appending:\n%s", opkg)
	}
	if !strings.Contains(opkg, "/etc/opkg/keys/") {
		t.Errorf("opkg feed does not install the key:\n%s", opkg)
	}
}

// The two tiers reach the host by completely different means, and the token
// exists so that one command line works on both. A regression here is a feed
// URL that resolves on a laptop and 404s in CI, or the reverse.
func TestResolveHostAnswersPerTier(t *testing.T) {
	const in = "http://" + HostToken + ":8080/packages/noarch/packages.adb"

	container := ResolveHost(in, false)
	if !strings.Contains(container, "host.docker.internal") {
		t.Errorf("container tier does not resolve to the docker host alias: %s", container)
	}

	vm := ResolveHost(in, true)
	if !strings.Contains(vm, "192.168.1.254") {
		t.Errorf("vm tier does not resolve to the SLIRP host address: %s", vm)
	}

	if strings.Contains(container, HostToken) || strings.Contains(vm, HostToken) {
		t.Error("the token survived substitution")
	}
}

// A real feed URL must come out byte-identical. Substitution that rewrites URLs
// it was not asked about is how a published feed silently becomes a local one.
func TestResolveHostLeavesARealURLAlone(t *testing.T) {
	const in = "https://repo.owfeed.org/releases/25.12/noarch/packages.adb"
	for _, vm := range []bool{false, true} {
		if got := ResolveHost(in, vm); got != in {
			t.Errorf("vm=%v: rewrote a real URL: %s", vm, got)
		}
	}
}

// The bug this filter exists for: one glob over `dist/*/` matches both formats,
// and before filtering every router got both. apk answered the .ipk with
// `v2 package format error` and failed the .apk in the same command with it.
func TestFilterFilesKeepsOnlyThisManagersFormat(t *testing.T) {
	mixed := []string{
		"dist/noarch/luci-theme-example-0.11.7-r1.apk",
		"dist/all/luci-theme-example_0.11.7-r1_all.ipk",
	}

	keep, skipped := FilterFiles(config.APK, mixed)
	if len(keep) != 1 || keep[0] != mixed[0] {
		t.Errorf("apk kept %v, want just the .apk", keep)
	}
	if len(skipped) != 1 || skipped[0] != mixed[1] {
		t.Errorf("apk skipped %v, want just the .ipk", skipped)
	}

	keep, skipped = FilterFiles(config.OPKG, mixed)
	if len(keep) != 1 || keep[0] != mixed[1] {
		t.Errorf("opkg kept %v, want just the .ipk", keep)
	}
	if len(skipped) != 1 || skipped[0] != mixed[0] {
		t.Errorf("opkg skipped %v, want just the .apk", skipped)
	}
}

// A single-release run must come out exactly as it did before the filter
// existed, or every workflow already written pays for a bug it never hit.
func TestFilterFilesLeavesASingleLineRunAlone(t *testing.T) {
	for _, tc := range []struct {
		pm    config.PackageManager
		files []string
	}{
		{config.APK, []string{"dist/noarch/a.apk", "dist/aarch64_generic/b.apk"}},
		{config.OPKG, []string{"dist/all/a.ipk", "dist/aarch64_generic/b.ipk"}},
	} {
		keep, skipped := FilterFiles(tc.pm, tc.files)
		if len(keep) != len(tc.files) || len(skipped) != 0 {
			t.Errorf("%s: kept %v, skipped %v", tc.pm, keep, skipped)
		}
	}
}

// Only the two package formats are ruled on. Anything else — a script, an
// archive a fixture unpacks — is not something owlab was asked to judge, and
// dropping it would break installs that work today.
func TestFilterFilesPassesUnknownExtensionsThrough(t *testing.T) {
	files := []string{"payload.tar.gz", "setup.sh", "extras"}
	for _, pm := range []config.PackageManager{config.APK, config.OPKG} {
		keep, skipped := FilterFiles(pm, files)
		if len(keep) != len(files) || len(skipped) != 0 {
			t.Errorf("%s: kept %v, skipped %v", pm, keep, skipped)
		}
	}
}

// A glob matching only the other line's format leaves this router with nothing.
// The caller has to be able to see that, because a package manager invoked with
// no arguments succeeds and would report an install that never happened.
func TestFilterFilesCanKeepNothing(t *testing.T) {
	keep, skipped := FilterFiles(config.APK, []string{"dist/all/a.ipk", "dist/all/b.ipk"})
	if len(keep) != 0 {
		t.Errorf("kept %v, want nothing", keep)
	}
	if len(skipped) != 2 {
		t.Errorf("skipped %v, want both", skipped)
	}
}

// The note names the file and the manager that refused it. `v2 package format
// error` is what apk says on its own, and it describes a byte sequence rather
// than the reason — which is that this router runs the other package manager.
func TestSkipNoteNamesTheFileAndTheManager(t *testing.T) {
	got := SkipNote(config.APK, "/tmp/luci-theme-example_0.11.7-r1_all.ipk")
	want := "owlab: skipping luci-theme-example_0.11.7-r1_all.ipk (this router uses apk)"
	if got != want {
		t.Errorf("SkipNote =\n  %q\nwant\n  %q", got, want)
	}
	if got := SkipNote(config.OPKG, "dist/noarch/x.apk"); got != "owlab: skipping x.apk (this router uses opkg)" {
		t.Errorf("SkipNote = %q", got)
	}
}

// The extension decides, whatever case it was written in: a build that emitted
// `.APK` would otherwise be silently handed to opkg.
func TestInstallableIgnoresExtensionCase(t *testing.T) {
	if !Installable(config.APK, "X.APK") {
		t.Error("apk refused an uppercase .APK")
	}
	if Installable(config.APK, "X.IPK") {
		t.Error("apk accepted an uppercase .IPK")
	}
}

// The measured output of a real `apk update` that lost one feed of nine, on
// openwrt/rootfs:x86_64-25.12.4 (2026-09-07). Trimmed to three of the eight
// bracketed lines; the count of them is what the shell reads, not their text.
const apkPartial = `ERROR: wget: exited with error 8
WARNING: updating and opening https://downloads.openwrt.org/releases/25.12.4/targets/x86/64/kmods/6.12.99-1-dead/packages.adb: unexpected end of file
 [https://downloads.openwrt.org/releases/25.12.4/targets/x86/64/packages/packages.adb]
 [https://downloads.openwrt.org/releases/25.12.4/packages/x86_64/base/packages.adb]
 [https://downloads.openwrt.org/releases/25.12.4/packages/x86_64/luci/packages.adb]
1 unavailable, 0 stale; 11279 distinct packages available`

// The same image with every feed pointed at an unresolvable host. Note the
// package count: 136 is the installed database, not a feed, which is why a
// count above zero cannot be the test on its own.
const apkDead = `wgetFailed to send request: Operation not permitted
ERROR: wget: exited with error 4
WARNING: updating and opening https://downloads.openwrt.org/releases/25.12.4/packages/x86_64/video/packages.adb: unexpected end of file
8 unavailable, 0 stale; 136 distinct packages available`

const apkOK = ` [https://downloads.openwrt.org/releases/25.12.4/targets/x86/64/packages/packages.adb]
 [https://downloads.openwrt.org/releases/25.12.4/packages/x86_64/base/packages.adb]
OK: 11279 distinct packages available`

// opkg on openwrt/rootfs:x86_64-24.10.8, one bad feed among seven good ones.
const opkgPartial = `Downloading https://downloads.openwrt.org/releases/24.10.8/packages/x86_64/base/Packages.gz
Updated list of available packages in /var/opkg-lists/openwrt_base
Updated list of available packages in /var/opkg-lists/openwrt_luci
Collected errors:
 * opkg_download: Failed to download https://downloads.openwrt.org/releases/24.10.8/targets/x86/64/kmods/6.6.999-1-deadbeef/Packages.gz, wget returned 8.`

// The same image with the network cut.
const opkgDead = `Downloading https://downloads.openwrt.org/releases/24.10.8/packages/x86_64/base/Packages.gz
*** Failed to download the package list from https://downloads.openwrt.org/releases/24.10.8/packages/x86_64/base/Packages.gz`

// runUpdate runs the generated refresh under `set -eu` — the way every caller
// runs it — against a stub manager that replays measured output and exits rc.
func runUpdate(t *testing.T, pm config.PackageManager, out string, rc int) (int, string) {
	t.Helper()
	bin := t.TempDir()
	stub := "#!/bin/sh\ncat <<'OUT'\n" + out + "\nOUT\nexit " + strconv.Itoa(rc) + "\n"
	name := "opkg"
	if pm == config.APK {
		name = "apk"
	}
	if err := os.WriteFile(filepath.Join(bin, name), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	// Run the generated shell through a real one, because what is being tested is
	// how it behaves under `set -e` -- not what string it is. A rewrite that reads
	// correctly and still exits at the wrong line is exactly the bug this covers.
	//
	// Skipped where there is no POSIX shell to run it in. The one place that is
	// true is a Windows host, and owlab there is a client: it drives an engine and
	// never executes this text itself, which runs inside a router. Asserting on
	// the string instead would pass while the shell was broken, and that is worse
	// than a platform saying plainly it cannot check this.
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("no POSIX shell on this host, so the generated shell cannot be run: %v", err)
	}
	cmd := exec.Command(sh, "-c", "set -eu\n"+UpdateShell(pm))
	// Prepended, not replaced: the shell still needs grep, sed and printf.
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	got, err := cmd.CombinedOutput()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return code, string(got)
}

// The bug: one unreachable feed out of nine ended the whole install before a
// single package was tried. apk calls that condition recoverable and so does
// this now — but not quietly, because a package missing later has to be
// traceable to the feed that did not answer.
func TestPartialRefreshContinuesAndNamesTheFeed(t *testing.T) {
	code, out := runUpdate(t, config.APK, apkPartial, 1)
	if code != 0 {
		t.Fatalf("a partial refresh ended the install with %d:\n%s", code, out)
	}
	if !strings.Contains(out, "partial refresh, 3 feed(s) read, 11279 packages available") {
		t.Errorf("no summary of what did work:\n%s", out)
	}
	if !strings.Contains(out, "kmods/6.12.99-1-dead/packages.adb") {
		t.Errorf("the feed that did not answer is not named:\n%s", out)
	}

	code, out = runUpdate(t, config.OPKG, opkgPartial, 1)
	if code != 0 {
		t.Fatalf("opkg: a partial refresh ended the install with %d:\n%s", code, out)
	}
	if !strings.Contains(out, "partial refresh, 2 feed(s) read") {
		t.Errorf("opkg: no summary of what did work:\n%s", out)
	}
	if !strings.Contains(out, "kmods/6.6.999-1-deadbeef") {
		t.Errorf("opkg: the feed that did not answer is not named:\n%s", out)
	}
}

// No feed answered at all. Installing from an index that was never read would
// fail every package afterwards with a message about the package instead of
// the network, so this still ends the run, with the manager's own exit code.
func TestNoFeedAnsweredStillFails(t *testing.T) {
	code, out := runUpdate(t, config.APK, apkDead, 8)
	if code != 8 {
		t.Fatalf("exit %d, want apk's own 8:\n%s", code, out)
	}
	if !strings.Contains(out, "apk update failed: no feed answered") {
		t.Errorf("does not say what failed:\n%s", out)
	}

	code, out = runUpdate(t, config.OPKG, opkgDead, 7)
	if code != 7 {
		t.Fatalf("opkg: exit %d, want opkg's own 7:\n%s", code, out)
	}
	if !strings.Contains(out, "opkg update failed: no feed answered") {
		t.Errorf("opkg: does not say what failed:\n%s", out)
	}
}

// 136 packages "available" with every feed unresolvable is the installed
// database talking. Taking the summary line at its word — the first fix that
// suggests itself — would install from an index nothing refreshed.
func TestPackagesAvailableAloneIsNotEnough(t *testing.T) {
	if !strings.Contains(apkDead, "distinct packages available") {
		t.Fatal("fixture no longer carries the summary line this guards against")
	}
	if code, out := runUpdate(t, config.APK, apkDead, 8); code == 0 {
		t.Errorf("a refresh that read no feed was accepted:\n%s", out)
	}
}

// A non-network failure prints no summary line at all: nothing was counted, so
// nothing licenses continuing.
func TestRefreshWithNoSummaryLineFails(t *testing.T) {
	code, out := runUpdate(t, config.APK, "ERROR: unable to select packages:\n  world[bad-pin]", 99)
	if code != 99 {
		t.Errorf("exit %d, want 99:\n%s", code, out)
	}
}

// The normal path, which must stay as quiet as it was before: an index refresh
// reports mirrors and signature checks that say nothing about the install.
func TestCleanRefreshSaysNothing(t *testing.T) {
	for _, pm := range []config.PackageManager{config.APK, config.OPKG} {
		out := apkOK
		if pm == config.OPKG {
			out = opkgPartial
		}
		code, got := runUpdate(t, pm, out, 0)
		if code != 0 || got != "" {
			t.Errorf("%s: exit %d, output %q, want a silent success", pm, code, got)
		}
	}
}

// images/Dockerfile installs the container tier's packages and cannot call Go,
// so it carries the same shell as text. This is the check that keeps the two
// tiers refreshing the index identically — the whole reason the commands live
// in this package rather than at each call site.
func TestDockerfileRefreshesTheIndexTheSameWay(t *testing.T) {
	b, err := fs.ReadFile(owlab.BuildContext(), "Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	// Unwrap the RUN line continuations, so a `\` + newline + tabs reads as
	// the single space that separates two commands joined by "; ".
	flat := shellWords(strings.ReplaceAll(string(b), "\\\n", " "))
	for _, pm := range []config.PackageManager{config.APK, config.OPKG} {
		want := shellWords(strings.ReplaceAll(strings.TrimSuffix(UpdateShell(pm), "\n"), "\n", "; "))
		if !strings.Contains(flat, want) {
			t.Errorf("images/Dockerfile does not run the %s refresh this package generates.\nwant:\n%s", pm, want)
		}
	}
}

// shellWords collapses every run of whitespace to one space, which is the only
// difference the Dockerfile's indentation is allowed to make.
func shellWords(s string) string { return strings.Join(strings.Fields(s), " ") }
