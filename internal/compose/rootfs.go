package compose

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"owfeed.org/owlab/internal/config"
)

// tarballDir is where, inside the build context, the rootfs tarballs go: one
// <router-id>.tar.gz per router built from one. The Dockerfile's tarball stage
// ADDs it from there.
const tarballDir = "tarball"

// fetchRootfs stages the rootfs tarball of each router built from one.
//
// On the host rather than with `ADD <url>` in the image. ADD does not unpack a
// remote archive, so the stage that unpacked it ran tar in alpine, pulled from
// Docker Hub: the path owlab falls back to when a registry does not answer
// pulled from a registry itself. A local archive ADD unpacks onto scratch,
// with no base image at all.
func fetchRootfs(cfg *config.Config, ctxDir, cacheDir string) error {
	for i := range cfg.Routers {
		r := &cfg.Routers[i]
		if r.Fidelity == config.VM || r.BaseImage() != "" {
			continue
		}
		url := r.RootfsTarballURL()
		if r.Rolling() {
			// Fetch keeps a URL forever, and a snapshot's tarball is rebuilt
			// under the same name: a cached one would hold the router at the
			// day it was first fetched, behind its own feed.
			if err := os.Remove(cachePath(url, cacheDir)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
		cached, err := Fetch(url, cacheDir)
		if err != nil {
			return fmt.Errorf("%s: the rootfs tarball, which this router is built from: %w", r.ID, err)
		}
		dst := filepath.Join(ctxDir, tarballDir, r.ID+".tar.gz")
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := copyFile(cached, dst); err != nil {
			return err
		}
	}
	return nil
}
