package pkgmgr

import (
	"strings"
	"testing"

	"github.com/VizzleTF/owlab/internal/config"
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
