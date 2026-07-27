package compose

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/VizzleTF/owlab/internal/config"
)

// fetchExtraPackages downloads every router's out-of-feed packages into the
// build context.
//
// The download happens on the HOST, not with a RUN inside the image, for two
// reasons. A stock OpenWrt rootfs has no curl and its busybox wget cannot do
// TLS, so an in-image download would depend on which package set the user
// happened to choose. And doing it here means a failed or truncated download
// is reported as itself, rather than as a confusing package-manager error
// three layers into a build.
//
// Files land under <context>/extra/<router-id>/, so each image installs only
// its own — a 24.10 router must not be handed a .apk built for 25.12.
func fetchExtraPackages(cfg *config.Config, ctxDir, cacheDir string) error {
	// Always present, even when empty: the Dockerfile COPYs it
	// unconditionally, and a COPY of a missing directory is a hard error.
	if err := os.MkdirAll(filepath.Join(ctxDir, "extra"), 0o755); err != nil {
		return err
	}
	for i := range cfg.Routers {
		r := &cfg.Routers[i]
		if r.Fidelity == config.VM || len(r.Extra) == 0 {
			continue
		}
		dst := filepath.Join(ctxDir, "extra", r.ID)
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return err
		}
		for _, e := range r.Extra {
			url := e.URLFor(r.PackageManager())
			if url == "" {
				// Nothing published for this package manager. That is a
				// legitimate shape — a package may be apk-only — so say so
				// and carry on rather than failing the build.
				fmt.Fprintf(os.Stderr, "owlab: %s: %s has no %s build, skipping\n",
					r.ID, e.Name, r.PackageManager())
				continue
			}
			cached, err := Fetch(url, cacheDir)
			if err != nil {
				return fmt.Errorf("%s: %s: %w", r.ID, e.Name, err)
			}
			if err := copyFile(cached, filepath.Join(dst, path.Base(url))); err != nil {
				return err
			}
		}
	}
	return nil
}

// Fetch downloads a URL into the cache, returning the cached path. A URL
// already in the cache is not re-fetched: these are release artifacts at
// immutable URLs, and re-downloading them on every `up` would be pure waste.
//
// Exported because the VM tier installs the same out-of-feed packages over
// ssh instead of through a build context, and both paths should share one
// cache rather than each keeping their own copy of the same file.
func Fetch(url, cacheDir string) (string, error) {
	sum := sha256.Sum256([]byte(url))
	name := hex.EncodeToString(sum[:8]) + "-" + path.Base(url)
	dst := filepath.Join(cacheDir, name)

	if st, err := os.Stat(dst); err == nil && st.Size() > 0 {
		return dst, nil
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}

	fmt.Fprintf(os.Stderr, "owlab: fetching %s\n", url)
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %s", url, resp.Status)
	}

	// Written to a temporary name and renamed, so an interrupted download
	// cannot be mistaken for a complete one on the next run.
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return "", err
	}
	return dst, nil
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}
