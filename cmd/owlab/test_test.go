package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
