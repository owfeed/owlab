package qemu

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/VizzleTF/owlab/internal/config"
)

// CacheDir is where downloaded disk images live.
//
// A user cache directory rather than the project's .owlab, because these are
// large (a quarter of a gigabyte each, uncompressed) and are shared by every
// project on the machine that boots the same release. Deleting it costs one
// download, never any state: the images are read-only backing files and all
// per-router writes go to the qcow2 overlay in the project.
func CacheDir() string {
	if env := os.Getenv("OWLAB_CACHE"); env != "" {
		return env
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "owlab")
	}
	return filepath.Join(base, "owlab", "images")
}

// EnsureImage downloads and unpacks a router's disk image if it is not
// already cached, and returns the path to the raw image.
func EnsureImage(ctx context.Context, r *config.Router, progress io.Writer) (string, error) {
	dir := CacheDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	raw := filepath.Join(dir, strings.TrimSuffix(r.VMImageName(), ".gz"))
	if st, err := os.Stat(raw); err == nil && st.Size() > 0 {
		return raw, nil
	}

	url := r.VMImageURL()
	if progress != nil {
		fmt.Fprintf(progress, "  downloading %s\n", r.VMImageName())
	}

	want, err := expectedSum(ctx, r)
	if err != nil && progress != nil {
		// Not fatal. A missing or unparseable sha256sums file should not
		// stand between a developer and a router — it costs the integrity
		// check, which is stated rather than skipped silently.
		fmt.Fprintf(progress, "  ! no checksum available (%v) — the download is not verified\n", err)
	}

	gz := raw + ".gz.part"
	sum, err := fetchTo(ctx, url, gz)
	if err != nil {
		os.Remove(gz)
		return "", err
	}
	if want != "" && sum != want {
		os.Remove(gz)
		return "", fmt.Errorf("checksum mismatch for %s\n  want %s\n  got  %s", r.VMImageName(), want, sum)
	}

	// Unpacked to a temporary name and renamed, so an interrupted run can
	// never leave a half-written image that the next run treats as cached.
	tmp := raw + ".part"
	if err := gunzip(gz, tmp); err != nil {
		os.Remove(gz)
		os.Remove(tmp)
		return "", err
	}
	os.Remove(gz)
	if err := os.Rename(tmp, raw); err != nil {
		return "", err
	}
	return raw, nil
}

// expectedSum reads the published checksum for this router's image.
//
// The checksum file comes from the same server over the same TLS connection as
// the image, so this catches a truncated download or a broken mirror — not a
// compromised download server. Upstream also publishes sha256sums.asc signed
// with the release key; verifying that needs the keyring, which is a trust
// decision beyond what a dev tool should make on the user's behalf.
func expectedSum(ctx context.Context, r *config.Router) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.SumsURL(), nil)
	if err != nil {
		return "", err
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", r.SumsURL(), resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	name := r.VMImageName()
	for _, line := range strings.Split(string(body), "\n") {
		sum, file, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		// The format is "<sum> *<file>" — the star marks binary mode.
		if strings.TrimPrefix(strings.TrimSpace(file), "*") == name {
			return sum, nil
		}
	}
	return "", fmt.Errorf("%s is not listed in sha256sums", name)
}

// fetchTo downloads a URL to a file and returns its sha256.
func fetchTo(ctx context.Context, url, dest string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := (&http.Client{Timeout: 30 * time.Minute}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return "", fmt.Errorf("%s: not found.\n\nThis usually means the release or the target does not publish this image — check the release number in %s", url, config.FileName)
		}
		return "", fmt.Errorf("%s: %s", url, resp.Status)
	}

	f, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), resp.Body); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func gunzip(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	zr, err := gzip.NewReader(in)
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(src), err)
	}
	defer zr.Close()
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, zr); err != nil { //nolint:gosec // upstream image, size is the point
		return err
	}
	return out.Close()
}
