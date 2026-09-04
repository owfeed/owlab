package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"owfeed.org/owlab/internal/config"
)

// A workflow writes `releases: "25.12.4 24.10.8"` in one YAML scalar, and a
// person writes the flag twice. Both have to work.
func TestStringListValues(t *testing.T) {
	var l stringList
	_ = l.Set("25.12.4 24.10.8")
	_ = l.Set("23.05.5,22.03.6")
	got := l.values()
	want := []string{"25.12.4", "24.10.8", "23.05.5", "22.03.6"}
	if len(got) != len(want) {
		t.Fatalf("values() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("values() = %v, want %v", got, want)
		}
	}
}

func TestResolveInstalls(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, "dist", "luci-app-mine-1.2.3-r1.apk")
	if err := os.MkdirAll(filepath.Dir(pkg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pkg, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The version in the file name is not known to whoever writes the
	// workflow, so the glob has to be expanded here rather than by a shell
	// that may never have seen it.
	files, err := resolveInstalls(dir, []string{"dist/luci-app-mine-*.apk"})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != pkg {
		t.Fatalf("files = %v, want [%s]", files, pkg)
	}

	// A glob matching nothing is the failure mode worth catching: it means the
	// build step produced nothing, and installing "nothing" would pass.
	if _, err := resolveInstalls(dir, []string{"dist/*.ipk"}); err == nil {
		t.Error("want an error when a glob matches no files")
	}
	// So is a path with a typo, which must not be taken for a feed name.
	if _, err := resolveInstalls(dir, []string{"dist/typo.apk"}); err == nil {
		t.Error("want an error for a missing file")
	}
	// A bare name is a package in a feed and stays one.
	files, err = resolveInstalls(dir, []string{"luci-app-sqm"})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Errorf("a feed name was taken for a file: %v", files)
	}
}

func TestFeedNames(t *testing.T) {
	in := []string{"luci-app-sqm", "dist/mine.apk", "dist/*.apk"}
	files := []string{"/abs/dist/mine.apk"}
	got := feedNames(in, files)
	if len(got) != 1 || got[0] != "luci-app-sqm" {
		t.Errorf("feedNames = %v, want [luci-app-sqm]", got)
	}
}

func TestLastMeaningfulLine(t *testing.T) {
	out := "fetch https://downloads.openwrt.org/...\nERROR: unable to select packages:\n  luci-app-mine-1.0: breaks: world[luci]\n\n"
	if got := lastMeaningfulLine(out); !strings.Contains(got, "breaks: world") {
		t.Errorf("lastMeaningfulLine = %q", got)
	}
	if got := lastMeaningfulLine("\n\n"); got == "" {
		t.Error("an empty log must still produce something to print")
	}
}

// recordingExec is a stand-in for the tier's Exec that remembers what it was
// asked to run, so a test can see what a router was actually sent.
type recordingExec struct {
	pushes  int
	scripts []string
}

func (e *recordingExec) run(_ context.Context, script string, stdin []byte, _ io.Writer) error {
	if stdin != nil {
		e.pushes++
	}
	e.scripts = append(e.scripts, script)
	return nil
}

// The issue this fixes: `--install 'dist/*/luci-theme-example*'` expands on the
// host, so both formats reached both routers. apk answered the .ipk with
// `v2 package format error` and, because everything goes into one command, the
// .apk that would have installed failed with it.
func TestTestInstallSendsEachRouterOnlyItsOwnFormat(t *testing.T) {
	dir := t.TempDir()
	apkFile := filepath.Join(dir, "luci-theme-example-0.11.7-r1.apk")
	ipkFile := filepath.Join(dir, "luci-theme-example_0.11.7-r1_all.ipk")
	for _, f := range []string{apkFile, ipkFile} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files := []string{apkFile, ipkFile}
	a := &app{cfg: &config.Config{Dir: dir}}

	for _, tc := range []struct {
		release, want, gone string
	}{
		{"25.12.5", filepath.Base(apkFile), filepath.Base(ipkFile)},
		{"24.10.8", filepath.Base(ipkFile), filepath.Base(apkFile)},
	} {
		r := &config.Router{ID: "r", Release: tc.release}
		e := &recordingExec{}
		res := a.testInstall(context.Background(), r, e.run, files, nil, nil)

		if !res.OK {
			t.Fatalf("%s: install failed: %s", tc.release, res.Detail)
		}
		// One push, not two: filtering happens before the file is put on the
		// wire, so no router is handed bytes it cannot read.
		if e.pushes != 1 {
			t.Errorf("%s: pushed %d files, want 1", tc.release, e.pushes)
		}
		installCmd := strings.Join(e.scripts, "\n")
		if !strings.Contains(installCmd, tc.want) {
			t.Errorf("%s: %s never reached the router:\n%s", tc.release, tc.want, installCmd)
		}
		if strings.Contains(installCmd, tc.gone) {
			t.Errorf("%s: %s was sent anyway:\n%s", tc.release, tc.gone, installCmd)
		}
		// Named out loud on the passing line. A file quietly dropped turns a
		// glob that matched nothing usable into an invisible no-op.
		if !strings.Contains(res.Detail, "owlab: skipping "+tc.gone+" (this router uses "+string(r.PackageManager())+")") {
			t.Errorf("%s: skip not reported: %q", tc.release, res.Detail)
		}
	}
}

// A single-release run has to be byte-identical to what it was before the
// filter existed: one push, no skip note, the same install line.
func TestTestInstallLeavesASingleLineRunAlone(t *testing.T) {
	dir := t.TempDir()
	apkFile := filepath.Join(dir, "luci-theme-example-0.11.7-r1.apk")
	if err := os.WriteFile(apkFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &app{cfg: &config.Config{Dir: dir}}
	e := &recordingExec{}
	res := a.testInstall(context.Background(), &config.Router{ID: "r", Release: "25.12.5"}, e.run, []string{apkFile}, nil, nil)

	if !res.OK || res.Detail != "" {
		t.Errorf("ok=%v detail=%q, want a clean pass", res.OK, res.Detail)
	}
	if res.Check != "install "+filepath.Base(apkFile) {
		t.Errorf("check line = %q", res.Check)
	}
	if e.pushes != 1 {
		t.Errorf("pushed %d files, want 1", e.pushes)
	}
}

// A glob matching only the other line's format leaves this router with nothing.
// Reported as a skip, never run: a package manager invoked with no arguments
// succeeds, so installing nothing would be a green line claiming the package
// went on, and the assertions after it would blame the router.
func TestTestInstallReportsARouterLeftWithNothing(t *testing.T) {
	dir := t.TempDir()
	ipkFile := filepath.Join(dir, "luci-theme-example_0.11.7-r1_all.ipk")
	if err := os.WriteFile(ipkFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &app{cfg: &config.Config{Dir: dir}}
	e := &recordingExec{}
	res := a.testInstall(context.Background(), &config.Router{ID: "r", Release: "25.12.5"}, e.run, []string{ipkFile}, nil, nil)

	if len(e.scripts) != 0 {
		t.Errorf("ran something on a router with nothing to install: %v", e.scripts)
	}
	if !strings.Contains(res.Check, "nothing for apk") {
		t.Errorf("check line does not say the router was skipped: %q", res.Check)
	}
	if !strings.Contains(res.Detail, "owlab: skipping "+filepath.Base(ipkFile)+" (this router uses apk)") {
		t.Errorf("skip not reported: %q", res.Detail)
	}
}
