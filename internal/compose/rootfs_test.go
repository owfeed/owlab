package compose

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	owlab "owfeed.org/owlab"
)

// A router built from a tarball finds it at the path the Dockerfile ADDs, and
// a router pulled from a registry gets none staged.
func TestFetchRootfsStagesTheTarballOfARouterBuiltFromOne(t *testing.T) {
	cfg := load(t, `
version: 1
project:
  name: demo
routers:
  - id: imm
    distro: immortalwrt
    release: "24.10.4"
    arch: x86_64
  - id: owrt
    release: "25.12.5"
    arch: x86_64
  - id: down
    release: "24.10.8"
    arch: x86_64
`)
	imm, _ := cfg.Router("imm")
	down, _ := cfg.Router("down")
	// What resolveBaseImages does when the registry does not answer.
	down.UseTarball()

	// Seeded, so the test reaches no download server: Fetch serves a URL it
	// has cached.
	cacheDir := t.TempDir()
	for _, r := range []string{imm.RootfsTarballURL(), down.RootfsTarballURL()} {
		if err := os.WriteFile(cachePath(r, cacheDir), []byte("tarball of "+r), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctxDir := t.TempDir()
	if err := fetchRootfs(cfg, ctxDir, cacheDir); err != nil {
		t.Fatal(err)
	}

	for _, r := range []struct{ id, url string }{{"imm", imm.RootfsTarballURL()}, {"down", down.RootfsTarballURL()}} {
		got, err := os.ReadFile(filepath.Join(ctxDir, tarballDir, r.id+".tar.gz"))
		if err != nil || string(got) != "tarball of "+r.url {
			t.Errorf("%s: staged %q, %v; want its own tarball", r.id, got, err)
		}
	}
	if _, err := os.Stat(filepath.Join(ctxDir, tarballDir, "owrt.tar.gz")); err == nil {
		t.Error("owrt pulls its image, and still had a tarball staged")
	}
}

// The tarball path exists for when a registry cannot be reached, so nothing on
// it may come from one: every FROM is the upstream image (scratch on the
// tarball path), scratch, or a stage. The alpine stage that used to unpack the
// tarball pulled from Docker Hub.
func TestDockerfilePullsNothingOnTheTarballPath(t *testing.T) {
	// The embedded copy, which is the one that ships.
	b, err := fs.ReadFile(owlab.BuildContext(), "Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	stages := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^FROM\s+(\S+)(?:\s+AS\s+(\S+))?`).FindAllStringSubmatch(string(b), -1) {
		switch from := m[1]; {
		case from == "${BASE_IMAGE}", from == "scratch", from == "${BASE_STAGE}", stages[from]:
		default:
			t.Errorf("FROM %s: the tarball path must not pull from a registry", from)
		}
		if m[2] != "" {
			stages[m[2]] = true
		}
	}
	// The Dockerfile and fetchRootfs name the same place.
	if want := "ADD " + tarballDir + "/${ROUTER_ID}.tar.gz /"; !strings.Contains(string(b), want) {
		t.Errorf("images/Dockerfile has no %q, which is where fetchRootfs stages the tarball", want)
	}
}
