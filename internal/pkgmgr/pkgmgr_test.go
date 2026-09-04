package pkgmgr

import (
	"strings"
	"testing"

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
