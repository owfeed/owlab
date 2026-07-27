package compose

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/VizzleTF/owlab/internal/config"
)

// CompliancePath is where the bundle lands inside the image.
const CompliancePath = "/usr/share/owlab/compliance"

// fetchCompliance stages the GPL compliance material for each router.
//
// Publishing an image built from an OpenWrt rootfs redistributes GPL'd
// binaries, which triggers GPLv2 §3: the corresponding source has to be
// offered. Upstream makes that cheap — beside every rootfs tarball it
// publishes the package manifest, a CycloneDX SBOM carrying per-package
// licences, and buildinfo files pinning the tree and every feed to an exact
// commit. What it does NOT publish is any of that INSIDE the rootfs: the
// tarball contains no licence texts and no copyright notices at all.
//
// So the bundle is assembled here and baked in at a known path, which is the
// difference between an image that documents its own provenance and one that
// leaves every downstream user to reconstruct it.
//
// Best effort by design: a fork that publishes no buildinfo, or a snapshot
// whose files moved, must not fail a developer's build. What is missing is
// recorded in the bundle's own README rather than silently omitted.
func fetchCompliance(cfg *config.Config, ctxDir, cacheDir string) error {
	root := filepath.Join(ctxDir, "compliance")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}

	for i := range cfg.Routers {
		r := &cfg.Routers[i]
		if r.Fidelity == config.VM {
			continue
		}
		dst := filepath.Join(root, r.ID)
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return err
		}

		base := r.ReleaseDir()
		stem := artifactStem(r)
		want := []string{
			stem + ".manifest",
			stem + ".bom.cdx.json",
			"config.buildinfo",
			"feeds.buildinfo",
			"version.buildinfo",
			"sha256sums",
			"sha256sums.asc",
		}

		var got, missing []string
		for _, name := range want {
			body, err := fetchSmall(base+"/"+name, cacheDir)
			if err != nil {
				missing = append(missing, name)
				continue
			}
			// sha256sums lists every artifact for the target and is mostly
			// noise here; keep only the lines naming this rootfs.
			if name == "sha256sums" {
				body = []byte(filterLines(string(body), stem))
			}
			if err := os.WriteFile(filepath.Join(dst, name), body, 0o644); err != nil {
				return err
			}
			got = append(got, name)
		}

		readme := complianceReadme(r, base, got, missing)
		if err := os.WriteFile(filepath.Join(dst, "README"), []byte(readme), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// artifactStem is the filename prefix upstream uses for this release/target,
// e.g. "openwrt-25.12.4-armsr-armv8".
func artifactStem(r *config.Router) string {
	url := r.RootfsTarballURL()
	name := url[strings.LastIndex(url, "/")+1:]
	return strings.TrimSuffix(name, "-rootfs.tar.gz")
}

func filterLines(s, substr string) string {
	var keep []string
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, substr) {
			keep = append(keep, line)
		}
	}
	return strings.Join(keep, "\n") + "\n"
}

// fetchSmall gets a small text artifact, with the same cache as the package
// downloader. Errors are ordinary here — plenty of these files do not exist
// for every distro and release.
func fetchSmall(url, cacheDir string) ([]byte, error) {
	path, err := download(url, cacheDir)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func complianceReadme(r *config.Router, base string, got, missing []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `%s %s (%s) — provenance and licence information

This image contains unmodified %s software, redistributed under the GPL and
the other licences its authors chose. Nothing here was built by owlab; the
root filesystem is upstream's own, unpacked or pulled as published.

Where it came from
  rootfs   %s
  feeds    %s
  built by owlab (https://github.com/VizzleTF/owlab)

Files in this directory
`, r.Title(), r.Release, r.Arch, r.Title(), r.RootfsTarballURL(), r.FeedBase())

	for _, f := range got {
		switch f {
		case "config.buildinfo":
			fmt.Fprintf(&b, "  config.buildinfo    the build configuration used\n")
		case "feeds.buildinfo":
			fmt.Fprintf(&b, "  feeds.buildinfo     every package feed pinned to an exact commit\n")
		case "version.buildinfo":
			fmt.Fprintf(&b, "  version.buildinfo   the source revision\n")
		case "sha256sums", "sha256sums.asc":
			fmt.Fprintf(&b, "  %-19s upstream checksum%s\n", f, map[bool]string{true: " signature"}[strings.HasSuffix(f, ".asc")])
		default:
			switch {
			case strings.HasSuffix(f, ".manifest"):
				fmt.Fprintf(&b, "  %-19s every package installed, with versions\n", f)
			case strings.HasSuffix(f, ".bom.cdx.json"):
				fmt.Fprintf(&b, "  %-19s CycloneDX SBOM, including per-package licences\n", f)
			}
		}
	}
	if len(missing) > 0 {
		fmt.Fprintf(&b, "\nNot published for this release: %s\n", strings.Join(missing, ", "))
	}

	fmt.Fprintf(&b, `
Written offer
  The complete corresponding source for the GPL'd software in this image is
  the %s tree at the revision in version.buildinfo, with the feeds at the
  commits in feeds.buildinfo and the configuration in config.buildinfo. It is
  published at the download server above, which retains past releases.

Trademarks
  owlab is not affiliated with, endorsed by, or sponsored by the OpenWrt
  project or the Software Freedom Conservancy, which owns the OpenWrt
  trademark, nor by any fork named here.
`, r.Title())
	return b.String()
}
