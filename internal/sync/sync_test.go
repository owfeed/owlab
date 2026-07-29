package sync

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"owfeed.org/owlab/internal/config"
)

// members lists what an archive would unpack, by destination path.
func members(t *testing.T, archive []byte) map[string]string {
	t.Helper()
	out := map[string]string{}
	tr := tar.NewReader(bytes.NewReader(archive))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		out["/"+hdr.Name] = string(body)
	}
}

func project(t *testing.T, files map[string]string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	body := "version: 1\nproject:\n  name: demo\nrouters:\n  - id: owrt\n    release: \"25.12.4\"\n    arch: x86_64\n"
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

// The default install mapping is luci.mk's own, so a standard LuCI package
// laid out the upstream way needs no install: block at all.
func TestBuildArchiveUsesTheDefaultInstallMapping(t *testing.T) {
	cfg := project(t, map[string]string{
		"htdocs/luci-static/theme/cascade.css": "body{}",
		"ucode/theme.uc":                       "{{ 1 }}",
		"luasrc/controller/x.lua":              "-- x",
		"root/etc/init.d/thing":                "#!/bin/sh",
	})

	archive, count, size, err := buildArchive(cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	if count != 4 {
		t.Errorf("count = %d, want 4", count)
	}
	if size == 0 {
		t.Error("size = 0")
	}

	got := members(t, archive)
	for _, want := range []string{
		"/www/luci-static/theme/cascade.css",
		"/usr/share/ucode/luci/theme.uc",
		"/usr/lib/lua/luci/controller/x.lua",
		"/etc/init.d/thing",
	} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing %s; archive holds %v", want, keys(got))
		}
	}
}

// Config files are the router's state, not the package's. A real install ships
// them as conffiles and leaves an existing one alone; overwriting them on every
// sync would throw away whatever the developer just configured.
func TestBuildArchiveSkipsRouterState(t *testing.T) {
	cfg := project(t, map[string]string{
		"root/etc/config/example":        "config example",
		"root/etc/uci-defaults/50-thing": "#!/bin/sh",
		"root/etc/init.d/thing":          "#!/bin/sh",
	})

	archive, count, _, err := buildArchive(cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1 (only the init script)", count)
	}
	got := members(t, archive)
	if _, ok := got["/etc/init.d/thing"]; !ok {
		t.Errorf("the init script should have been synced; archive holds %v", keys(got))
	}
	for _, skipped := range []string{"/etc/config/example", "/etc/uci-defaults/50-thing"} {
		if _, ok := got[skipped]; ok {
			t.Errorf("%s is router state and must not be synced", skipped)
		}
	}
}

// node_modules and the build directories are the ones that silently turn a
// 200 KB sync into a 200 MB one.
func TestBuildArchiveSkipsNoiseDirectories(t *testing.T) {
	cfg := project(t, map[string]string{
		"htdocs/app.js":                      "1",
		"htdocs/node_modules/dep/index.js":   "2",
		"htdocs/.git/config":                 "3",
		"htdocs/dist/bundle.js":              "4",
		"htdocs/.hidden/secret":              "5",
		"htdocs/sub/deep/legitimately/ok.js": "6",
	})

	archive, count, _, err := buildArchive(cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2; archive holds %v", count, keys(members(t, archive)))
	}
}

func TestIsConfigFile(t *testing.T) {
	yes := []string{"/etc/config/network", "/etc/uci-defaults/99-x"}
	no := []string{"/etc/init.d/thing", "/www/index.html", "/etc/configuration"}
	for _, p := range yes {
		if !isConfigFile(p) {
			t.Errorf("%s should be treated as router state", p)
		}
	}
	for _, p := range no {
		if isConfigFile(p) {
			t.Errorf("%s is package content, not router state", p)
		}
	}
}

// The permissions a file arrives with come from where it is going and what is
// in it, never from the source file — the host's own bits mean different
// things on each platform owlab runs on, and on two of them they mean nothing
// at all.
func TestModeFor(t *testing.T) {
	exec := []string{
		"/etc/init.d/thing",
		"/usr/bin/thing",
		"/usr/libexec/thing",
		"/etc/hotplug.d/iface/20-thing",
		"/usr/share/mypkg/helper.sh", // not an exec directory, but a script
	}
	data := []string{
		"/www/luci-static/theme/cascade.css",
		"/usr/share/ucode/luci/theme.uc",
		"/usr/lib/lua/luci/controller/x.lua",
	}
	for _, p := range exec {
		body := []byte("#!/bin/sh\n")
		if !strings.HasSuffix(p, ".sh") {
			body = []byte("no shebang here")
		}
		if got := ModeFor(p, body); got != 0o755 {
			t.Errorf("ModeFor(%s) = %o, want 755", p, got)
		}
	}
	for _, p := range data {
		if got := ModeFor(p, []byte("body{}")); got != 0o644 {
			t.Errorf("ModeFor(%s) = %o, want 644", p, got)
		}
	}
}

// The archive's modes must not depend on the mode of the file on disk. The
// project helper writes everything 0644, so an init script that comes out 0755
// can only have got there from the destination, and a CSS file that comes out
// 0644 proves the rule is not "everything is executable".
func TestArchiveModesIgnoreTheSourceFile(t *testing.T) {
	cfg := project(t, map[string]string{
		"root/etc/init.d/thing":                "#!/bin/sh",
		"htdocs/luci-static/theme/cascade.css": "body{}",
	})
	archive, _, _, err := buildArchive(cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{
		"/etc/init.d/thing":                  0o755,
		"/www/luci-static/theme/cascade.css": 0o644,
	}
	tr := tar.NewReader(bytes.NewReader(archive))
	seen := 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		w, ok := want["/"+hdr.Name]
		if !ok {
			continue
		}
		seen++
		if hdr.Mode != w {
			t.Errorf("%s arrives %o, want %o", hdr.Name, hdr.Mode, w)
		}
	}
	if seen != len(want) {
		t.Errorf("checked %d members, want %d", seen, len(want))
	}
}

// The theme clause is the one that matters: a half-synced theme is where LuCI
// crashes rather than falling back, so the script has to re-assert it — but
// only if the theme's files actually arrived.
func TestReloadScript(t *testing.T) {
	bare := ReloadScript("")
	for _, want := range []string{"/tmp/luci-indexcache", "/tmp/luci-modulecache", "rpcd reload"} {
		if !strings.Contains(bare, want) {
			t.Errorf("script does not drop %s:\n%s", want, bare)
		}
	}
	if strings.Contains(bare, "mediaurlbase") {
		t.Errorf("no theme was asked for:\n%s", bare)
	}

	themed := ReloadScript("footstrap")
	if !strings.Contains(themed, "mediaurlbase=/luci-static/footstrap") {
		t.Errorf("theme not asserted:\n%s", themed)
	}
	if !strings.Contains(themed, "[ -d /www/luci-static/footstrap ]") {
		t.Errorf("theme asserted without checking it is there:\n%s", themed)
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
