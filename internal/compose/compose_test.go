package compose

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"owfeed.org/owlab/internal/config"
	"owfeed.org/owlab/internal/engine"
)

// engineInfoForTest is a detected-nothing engine. Render reads it only for
// bind-mount ownership, which does not change what lands in the file.
func engineInfoForTest() engine.Info { return engine.Info{Kind: engine.Unknown} }

func TestProjectNameNormalises(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"luci-theme-footstrap", "owlab-luci-theme-footstrap"},
		{"LuCI_Theme", "owlab-luci-theme"},
		// Anything that is not a container-name character is dropped, not
		// substituted: a name with a slash in it must not become two segments.
		{"my project/v2", "owlab-myprojectv2"},
		{"--edges--", "owlab-edges"},
		{"", "owlab-project"},
		{"...", "owlab-project"},
	}
	for _, c := range cases {
		if got := ProjectName(c.in); got != c.want {
			t.Errorf("ProjectName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// ProjectName prefixes "owlab-", so feeding it its own output would name a
// container nothing can find. ContainerName exists to make that impossible.
func TestContainerNameTakesTheRawProjectName(t *testing.T) {
	got := ContainerName("My Project", "owrt2512")
	if want := "owlab-myproject-owrt2512"; got != want {
		t.Fatalf("ContainerName = %q, want %q", got, want)
	}
	if double := ContainerName(ProjectName("My Project"), "owrt2512"); double == got {
		t.Fatal("ProjectName is idempotent after all; the doc comment is now wrong")
	}
}

func load(t *testing.T, body string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, config.FileName)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// BuildArgsFor is the contract between `owlab up` and the publish workflow,
// which builds the same context with `docker buildx build`. A change here that
// nobody notices is how a published image drifts away from a local one.
func TestBuildArgsForUpstreamImage(t *testing.T) {
	cfg := load(t, `
version: 1
project:
  name: demo
  theme: footstrap
routers:
  - id: owrt
    release: "25.12.4"
    arch: x86_64
    packages: ["luci"]
    fixtures: ["networks"]
`)
	args := BuildArgsFor(cfg, &cfg.Routers[0])

	want := map[string]string{
		"BASE_STAGE":  "upstream",
		"BASE_IMAGE":  "openwrt/rootfs:x86_64-25.12.4",
		"PKG_MANAGER": "apk",
		"PACKAGES":    "luci",
		"FIXTURES":    "networks",
		"ROUTER_ID":   "owrt",
		"THEME":       "footstrap",
	}
	for k, v := range want {
		if args[k] != v {
			t.Errorf("%s = %q, want %q", k, args[k], v)
		}
	}
	if _, ok := args["ROOTFS_URL"]; ok {
		t.Error("ROOTFS_URL must not be set when building from a published image")
	}
}

func TestBuildArgsForTarball(t *testing.T) {
	// ImmortalWrt publishes no usable container image, so every one of its
	// routers takes the tarball path.
	cfg := load(t, `
version: 1
project:
  name: demo
routers:
  - id: imm
    distro: immortalwrt
    release: "24.10.1"
    arch: x86_64
`)
	args := BuildArgsFor(cfg, &cfg.Routers[0])

	if args["BASE_STAGE"] != "tarball" {
		t.Errorf("BASE_STAGE = %q, want tarball", args["BASE_STAGE"])
	}
	// buildkit resolves the metadata for every FROM before it works out which
	// stages the target needs, so BASE_IMAGE still has to name something that
	// resolves — and scratch costs nothing.
	if args["BASE_IMAGE"] != "scratch" {
		t.Errorf("BASE_IMAGE = %q, want scratch", args["BASE_IMAGE"])
	}
	if !strings.HasSuffix(args["ROOTFS_URL"], "immortalwrt-24.10.1-x86-64-rootfs.tar.gz") {
		t.Errorf("ROOTFS_URL = %q", args["ROOTFS_URL"])
	}
	// 24.10 predates the switch to apk.
	if args["PKG_MANAGER"] != "opkg" {
		t.Errorf("PKG_MANAGER = %q, want opkg", args["PKG_MANAGER"])
	}
}

// Render must not touch the network or wipe the build context — `owlab down`
// and `owlab logs` go through it, and both have to work offline.
func TestRenderWritesComposeAndLeavesContextAlone(t *testing.T) {
	cfg := load(t, `
version: 1
project:
  name: demo
routers:
  - id: owrt
    release: "25.12.4"
    arch: x86_64
  - id: box
    release: "25.12.4"
    arch: x86_64
    fidelity: vm
`)
	// A file left over from an earlier build. Render must leave it there;
	// Materialize is what wipes.
	ctxDir := filepath.Join(cfg.Dir, WorkDirName, "context")
	if err := os.MkdirAll(ctxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(ctxDir, "left-over")
	if err := os.WriteFile(marker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	proj, err := Render(cfg, engineInfoForTest())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("Render disturbed the build context: %v", err)
	}

	body, err := os.ReadFile(proj.ComposePath)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(body)
	if !strings.Contains(doc, "owlab-demo-owrt") {
		t.Errorf("compose file has no container for owrt:\n%s", doc)
	}
	// The VM tier spawns QEMU on the host and has no compose service at all.
	if strings.Contains(doc, "owlab-demo-box") {
		t.Errorf("compose file has a service for a fidelity-vm router:\n%s", doc)
	}
	// Without this a router cannot install from a feed served by the machine
	// running owlab, which is how a feed's own CI proves what it publishes works.
	if !strings.Contains(doc, "host.docker.internal:host-gateway") {
		t.Errorf("compose file gives the router no route back to the host:\n%s", doc)
	}
}
