package compose

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"time"

	"owfeed.org/owlab/internal/config"
	"owfeed.org/owlab/internal/netx"
)

// fetchExtraPackages downloads the out-of-feed packages of the routers being
// built into the build context.
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
func fetchExtraPackages(routers []*config.Router, ctxDir, cacheDir string) error {
	for _, r := range routers {
		if r.Fidelity == config.VM {
			continue
		}
		// Always present, even when empty: the Dockerfile COPYs this router's
		// directory unconditionally, and a COPY of a missing directory is a
		// hard error.
		dst := filepath.Join(ctxDir, "extra", r.ID)
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return err
		}
		for n, e := range r.Extra {
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
			// Numbered, because the image installs whatever the glob returns
			// and a glob is alphabetical. These packages depend on each other
			// — a luci-app-* needs the daemon it drives — and alphabetical order put the
			// dependent first, which apk rejects outright with "unable to
			// select packages". The prefix makes glob order the order the
			// developer wrote them in.
			staged := fmt.Sprintf("%02d-%s", n, path.Base(url))
			if err := copyFile(cached, filepath.Join(dst, staged)); err != nil {
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
//
// netx retries a failing server up to the response headers. A body that
// breaks off, or a server that stops sending, is retried here: the whole file
// again, netx.DefaultAttempts times, because these files are a few MB and a
// snapshot's can change between two attempts, which a resumed Range request
// would stitch together.
func Fetch(url, cacheDir string) (string, error) {
	dst := cachePath(url, cacheDir)
	if st, err := os.Stat(dst); err == nil && st.Size() > 0 {
		return dst, nil
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}

	fmt.Fprintf(os.Stderr, "owlab: fetching %s\n", url)
	// Written to a temporary name and renamed, so an interrupted download
	// cannot be mistaken for a complete one on the next run.
	tmp := dst + ".part"
	delay := netx.DefaultDelay
	for attempt := 1; ; attempt++ {
		retry, err := fetchOnce(url, tmp)
		if err == nil {
			break
		}
		os.Remove(tmp)
		if !retry {
			return "", err
		}
		if attempt >= netx.DefaultAttempts {
			return "", &netx.OutageError{Attempts: attempt, Err: err}
		}
		fmt.Fprintf(os.Stderr, "owlab: %v; asking again in %s (attempt %d of %d)\n",
			err, delay, attempt, netx.DefaultAttempts)
		time.Sleep(delay)
		delay *= 2
	}
	if err := os.Rename(tmp, dst); err != nil {
		return "", err
	}
	return dst, nil
}

// cachePath is where Fetch keeps url: the base name for whoever reads the
// directory, and a hash of the whole URL so two releases' files of the same
// name do not collide.
func cachePath(url, cacheDir string) string {
	sum := sha256.Sum256([]byte(url))
	return filepath.Join(cacheDir, hex.EncodeToString(sum[:8])+"-"+path.Base(url))
}

// StallAfter is how long a download may go without a byte before it counts as
// an outage and is asked again. A variable so tests can drop the waiting.
//
// An idle deadline rather than a total one. The total was 5 minutes, which
// failed a slow link that was still delivering and, on a server that had gone
// quiet, held a CI step for 5 minutes before failing it without a retry. A
// server that trickles a byte a minute is not caught; whatever runs owlab (a CI
// step's timeout) bounds that.
var StallAfter = 60 * time.Second

// errStalled is the cause a download is cancelled with when StallAfter passes
// without a byte.
var errStalled = errors.New("stalled")

// fetchOnce makes one attempt at url, writing the body to tmp. retry reports a
// failure netx did not already retry: the body broke off or the server stopped
// sending.
func fetchOnce(url, tmp string) (retry bool, err error) {
	// The same deadline on the headers, inside netx's attempts: a server that
	// accepts the connection and never answers is retried like one that refused.
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = StallAfter
	defer tr.CloseIdleConnections()
	client := netx.Client(&http.Client{Transport: tr})

	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, netx.Status(url, resp)
	}

	f, err := os.Create(tmp)
	if err != nil {
		return false, err
	}
	idle := time.AfterFunc(StallAfter, func() { cancel(errStalled) })
	defer idle.Stop()
	body := &idleReader{r: resp.Body, idle: idle}
	_, err = io.Copy(f, body)
	if cerr := f.Close(); err == nil && cerr != nil {
		return false, cerr
	}
	switch {
	case err == nil:
		return false, nil
	case errors.Is(context.Cause(ctx), errStalled):
		return true, fmt.Errorf("GET %s: the server sent nothing for %s", url, StallAfter)
	case body.err != nil:
		// A write error is the disk's, and asking the server again does not
		// fix it; only a read error can be the server's.
		return netx.Transient(body.err), fmt.Errorf("GET %s: reading the body: %w", url, body.err)
	default:
		return false, err
	}
}

// idleReader pushes the stall deadline back on every read that brings bytes,
// and keeps the read error so it can be told from a write error.
type idleReader struct {
	r    io.Reader
	idle *time.Timer
	err  error
}

func (r *idleReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 {
		r.idle.Reset(StallAfter)
	}
	if err != nil && err != io.EOF {
		r.err = err
	}
	return n, err
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}
