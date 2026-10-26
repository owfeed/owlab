package compose

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	owlab "owfeed.org/owlab"
	"owfeed.org/owlab/internal/config"
)

// A router built from a tarball finds it at the path the Dockerfile ADDs, a
// router pulled from a registry gets none staged, and neither does a tarball
// router that is not being built.
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
	owrt, _ := cfg.Router("owrt")
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
	staged := func(ctxDir, id string) (string, error) {
		got, err := os.ReadFile(filepath.Join(ctxDir, tarballDir, id+".tar.gz"))
		return string(got), err
	}

	// `owlab up owrt down`: imm is in owlab.yaml and is not being built. Its
	// tarball is a download that can fail, from a server this build has no
	// other use for.
	ctxDir := t.TempDir()
	if err := fetchRootfs([]*config.Router{owrt, down}, ctxDir, cacheDir); err != nil {
		t.Fatal(err)
	}
	if got, err := staged(ctxDir, "down"); err != nil || got != "tarball of "+down.RootfsTarballURL() {
		t.Errorf("down: staged %q, %v; want its own tarball", got, err)
	}
	if _, err := staged(ctxDir, "owrt"); err == nil {
		t.Error("owrt pulls its image, and still had a tarball staged")
	}
	if _, err := staged(ctxDir, "imm"); err == nil {
		t.Error("imm is not being built, and its tarball was staged")
	}

	// `owlab up imm`.
	ctxDir = t.TempDir()
	if err := fetchRootfs([]*config.Router{imm}, ctxDir, cacheDir); err != nil {
		t.Fatal(err)
	}
	if got, err := staged(ctxDir, "imm"); err != nil || got != "tarball of "+imm.RootfsTarballURL() {
		t.Errorf("imm: staged %q, %v; want its own tarball", got, err)
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
