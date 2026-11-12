package compose

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const inputsBase = `
version: 1
project:
  name: demo
  theme: footstrap
routers:
  - id: owrt
    release: "25.12.5"
    arch: x86_64
    packages: ["+luci-app-ttyd"]
    extra_packages:
      - name: luci-app-mine
        apk: https://example.org/v1/luci-app-mine-1.0-r1.apk
        ipk: https://example.org/v1/luci-app-mine_1.0-r1_all.ipk
`

// `owlab up --no-build` trusts an image only as far as this digest goes, so
// every input owlab.yaml can change has to move it, and nothing else may.
func TestInputsMoveWithWhatTheImageIsBuiltFrom(t *testing.T) {
	base := load(t, inputsBase)
	want := Inputs(base, &base.Routers[0])
	if !strings.HasPrefix(want, "sha256:") || len(want) != len("sha256:")+64 {
		t.Fatalf("Inputs = %q, want sha256:<64 hex>", want)
	}
	again := load(t, inputsBase)
	if got := Inputs(again, &again.Routers[0]); got != want {
		t.Errorf("the same owlab.yaml loaded twice gave two digests: %s, %s", want, got)
	}

	changed := map[string]string{
		"a package":           strings.Replace(inputsBase, `"+luci-app-ttyd"`, `"+luci-app-ttyd", "+luci-app-wol"`, 1),
		"an extra's URL":      strings.Replace(inputsBase, "v1/luci-app-mine-1.0-r1.apk", "v2/luci-app-mine-1.1-r1.apk", 1),
		"the release":         strings.Replace(inputsBase, `"25.12.5"`, `"25.12.4"`, 1),
		"the theme":           strings.Replace(inputsBase, "theme: footstrap", "theme: bootstrap", 1),
		"the router's id":     strings.Replace(inputsBase, "id: owrt", "id: other", 1),
		"the fixtures":        strings.Replace(inputsBase, `    arch: x86_64`, "    arch: x86_64\n    fixtures: [none]", 1),
		"the package manager": strings.Replace(inputsBase, `"25.12.5"`, `"24.10.8"`, 1),
	}
	for what, body := range changed {
		cfg := load(t, body)
		if got := Inputs(cfg, &cfg.Routers[0]); got == want {
			t.Errorf("changing %s left the digest as it was", what)
		}
	}

	// A build from the rootfs tarball, after the registry did not answer, is
	// another image: a --no-build that does not probe the registry must not
	// take it for the usual one.
	tb := load(t, inputsBase)
	tb.Routers[0].UseTarball()
	if got := Inputs(tb, &tb.Routers[0]); got == want {
		t.Error("a router built from the rootfs tarball has the digest of one built from the image")
	}

	// A router built from the tarball, as every ImmortalWrt one is, names its
	// release and its target nowhere in the build args.
	imm := `
version: 1
project:
  name: demo
routers:
  - id: imm
    distro: immortalwrt
    release: "24.10.3"
    arch: ARCH
`
	immA := load(t, strings.Replace(imm, "ARCH", "x86_64", 1))
	immB := load(t, strings.Replace(strings.Replace(imm, "ARCH", "x86_64", 1), `"24.10.3"`, `"24.10.4"`, 1))
	if Inputs(immA, &immA.Routers[0]) == Inputs(immB, &immB.Routers[0]) {
		t.Error("changing an ImmortalWrt release left the digest as it was")
	}
	armA := load(t, strings.Replace(imm, "ARCH", "arm_cortex-a15_neon-vfpv4", 1))
	armB := load(t, strings.Replace(imm, "ARCH", "arm_cortex-a9_vfpv3-d16", 1))
	if Inputs(armA, &armA.Routers[0]) == Inputs(armB, &armB.Routers[0]) {
		t.Error("two ARMv7 targets with one platform have one digest")
	}

	// The compliance files come from the release's directory even when the
	// base is a named image.
	img := strings.Replace(inputsBase, `    arch: x86_64`, "    arch: x86_64\n    image: ghcr.io/openwrt/rootfs:x86_64-25.12.5", 1)
	a := load(t, img)
	b := load(t, strings.Replace(img, `release: "25.12.5"`, `release: "25.12.4"`, 1))
	if Inputs(a, &a.Routers[0]) == Inputs(b, &b.Routers[0]) {
		t.Error("changing the release under a named image left the digest as it was")
	}

	// The project name and the ports are not what the image is built from.
	renamed := load(t, strings.Replace(inputsBase, "name: demo", "name: other", 1))
	if got := Inputs(renamed, &renamed.Routers[0]); got != want {
		t.Error("renaming the project changed the digest of an image it does not go into")
	}
}

// The label goes on the image, which is what a cache saves and `docker load`
// restores, and not only on the container.
func TestRenderLabelsTheImageWithItsInputs(t *testing.T) {
	cfg := load(t, inputsBase)
	proj, err := Render(cfg, engineInfoForTest())
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(proj.ComposePath)
	if err != nil {
		t.Fatal(err)
	}
	var doc File
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	svc := doc.Services["owrt"]
	if svc.Build == nil {
		t.Fatalf("no build stanza:\n%s", body)
	}
	if got, want := svc.Build.Labels[InputsLabel], Inputs(cfg, &cfg.Routers[0]); got != want {
		t.Errorf("build.labels[%s] = %q, want %q", InputsLabel, got, want)
	}
	if svc.Image != Image(cfg, &cfg.Routers[0]) || svc.Image != "owlab-demo-owrt:latest" {
		t.Errorf("image = %q, want owlab-demo-owrt:latest from Image()", svc.Image)
	}
}
