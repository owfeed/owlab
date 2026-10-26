package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"owfeed.org/owlab/internal/config"
)

// A file whose dependencies are already on the router installs without a feed.
// The index refresh this replaced made `owlab install <router> <file>` depend on
// every feed answering: with none answering, the refresh ended the script
// before the install ran.
func TestInstallOfflineAsksNoFeed(t *testing.T) {
	for _, pm := range []config.PackageManager{config.APK, config.OPKG} {
		var scripts []string
		run := func(_ context.Context, script string, _ []byte, out io.Writer) error {
			scripts = append(scripts, script)
			_, err := io.WriteString(out, "(1/1) Installing luci-theme-example (1.0-r1)\n")
			return err
		}
		var out bytes.Buffer
		if !installOffline(t.Context(), run, pm, []string{"/tmp/luci-theme-example"}, &out) {
			t.Errorf("%s: an install that succeeded was handed back:\n%s", pm, out.String())
		}
		if len(scripts) != 1 || strings.Contains(scripts[0], "update") {
			t.Errorf("%s: want one install and no refresh, ran %q", pm, scripts)
		}
		if pm == config.APK && !strings.Contains(scripts[0], "--no-network") {
			t.Errorf("apk without --no-network revalidates an index older than four hours first: %q", scripts[0])
		}
		if !strings.Contains(out.String(), "Installing luci-theme-example") {
			t.Errorf("%s: the package manager's output was not shown: %q", pm, out.String())
		}
	}
}

// A file that needs a dependency the router lacks fails offline; the caller then
// refreshes and installs again. That attempt's output is the one that says why,
// so this one is not shown, and the note says what happens next.
func TestInstallOfflineHandsBackAFileThatNeedsAFeed(t *testing.T) {
	run := func(_ context.Context, _ string, _ []byte, out io.Writer) error {
		_, _ = io.WriteString(out, "ERROR: unable to select packages:\n  ttyd-1.7.7-r1:\n    masked in: --no-network\n")
		return errors.New("exit status 6")
	}
	var out bytes.Buffer
	if installOffline(t.Context(), run, config.APK, []string{"/tmp/luci-app-ttyd.apk"}, &out) {
		t.Fatal("an install that failed was reported as done")
	}
	if strings.Contains(out.String(), "masked in") {
		t.Errorf("the offline attempt's own failure was shown: %q", out.String())
	}
	if !strings.Contains(out.String(), "refreshing its feeds and trying again") {
		t.Errorf("does not say what happens next: %q", out.String())
	}
}

// `owlab up` printed its table, a "did not answer" line under it, and exited 0,
// so a script that ran its checks after `owlab up` ran them against a router
// that was not serving.
func TestPrintReadyFailsForARouterThatDidNotAnswer(t *testing.T) {
	routers := []*config.Router{{ID: "a"}, {ID: "b"}}
	a := &app{}
	if err := a.printReady(routers, map[string]bool{"a": true}); !errors.Is(err, errNotReady) {
		t.Errorf("one router never answered: err = %v, want errNotReady", err)
	}
	if err := a.printReady(routers, map[string]bool{"a": true, "b": true}); err != nil {
		t.Errorf("every router answered: err = %v, want nil", err)
	}
	// --no-wait: nothing was waited for, so nothing is claimed either way.
	if err := a.printReady(routers, nil); err != nil {
		t.Errorf("--no-wait: err = %v, want nil", err)
	}
}
