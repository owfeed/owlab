package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"owfeed.org/owlab/internal/config"
	"owfeed.org/owlab/internal/netx"
	"owfeed.org/owlab/internal/upstream"
)

// fakeRegistry answers a manifest HEAD with status(path) and counts the asks.
// No token dance: ProbeImage takes a plain 200, 404 or 503 as it is.
func fakeRegistry(t *testing.T, status func(path string) int) (host string, hits *atomic.Int32) {
	t.Helper()
	hits = new(atomic.Int32)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(status(r.URL.Path))
	}))
	t.Cleanup(srv.Close)
	oldTransport, oldDelay := upstream.RegistryTransport, netx.DefaultDelay
	upstream.RegistryTransport, netx.DefaultDelay = srv.Client().Transport, time.Millisecond
	t.Cleanup(func() { upstream.RegistryTransport, netx.DefaultDelay = oldTransport, oldDelay })
	return strings.TrimPrefix(srv.URL, "https://"), hits
}

// fakeTarballs answers every HEAD of a rootfs tarball with err, and counts the
// asks: the real download server is not a unit test's to reach.
func fakeTarballs(t *testing.T, err error) (asked *atomic.Int32) {
	t.Helper()
	asked = new(atomic.Int32)
	old := headTarball
	headTarball = func(context.Context, string) error {
		asked.Add(1)
		return err
	}
	t.Cleanup(func() { headTarball = old })
	return asked
}

func loadRouters(t *testing.T, body string) []*config.Router {
	t.Helper()
	p := filepath.Join(t.TempDir(), config.FileName)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	routers, err := cfg.Select(nil)
	if err != nil {
		t.Fatal(err)
	}
	return routers
}

// The healthy direction: a registry that answers keeps every router on its
// image, and an explicit image that is not there is left for the build to
// name rather than quietly replaced.
func TestResolveBaseImagesKeepsTheImageWhileTheRegistryAnswers(t *testing.T) {
	host, _ := fakeRegistry(t, func(path string) int {
		if strings.HasSuffix(path, "/x86_64-25.12.5") {
			return http.StatusOK
		}
		return http.StatusNotFound
	})
	asked := fakeTarballs(t, nil)
	routers := loadRouters(t, `
version: 1
routers:
  - id: here
    release: "25.12.5"
    arch: x86_64
    image: `+host+`/openwrt/rootfs:x86_64-25.12.5
  - id: typo
    release: "25.12.5"
    arch: x86_64
    image: `+host+`/openwrt/rootfs:x86_64-25.12.55
`)
	resolveBaseImages(t.Context(), routers, false)
	for _, r := range routers {
		if r.BaseImage() != r.Image {
			t.Errorf("%s: base image %q, want its own %q", r.ID, r.BaseImage(), r.Image)
		}
	}
	if asked.Load() != 0 {
		t.Errorf("the download server was asked %d times on a run whose registry answered", asked.Load())
	}
}

// A registry that does not answer moves every router on it to the tarball,
// with the honest platform a tarball build carries, and is asked once.
func TestResolveBaseImagesBuildsFromTheTarballWhenTheRegistryIsDown(t *testing.T) {
	host, hits := fakeRegistry(t, func(string) int { return http.StatusServiceUnavailable })
	fakeTarballs(t, nil)
	routers := loadRouters(t, `
version: 1
routers:
  - id: arm
    release: "25.12.5"
    arch: aarch64_generic
    image: `+host+`/openwrt/rootfs:aarch64_generic-25.12.5
  - id: old
    release: "24.10.8"
    arch: x86_64
    image: `+host+`/openwrt/rootfs:x86_64-24.10.8
`)
	resolveBaseImages(t.Context(), routers, false)
	for _, r := range routers {
		if r.BaseImage() != "" {
			t.Errorf("%s still pulls %q from a registry that did not answer", r.ID, r.BaseImage())
		}
	}
	if got := routers[0].Platform(); got != "linux/arm64" {
		t.Errorf("platform %q, want the tarball's honest linux/arm64", got)
	}
	if int(hits.Load()) != netx.DefaultAttempts {
		t.Errorf("the registry was asked %d times, want netx's %d for both routers together", hits.Load(), netx.DefaultAttempts)
	}
}

// Offline, or online only through the engine's proxy: the registry and the
// download server both fail from here. The tarball cannot be fetched, so the
// image stays for the engine, which may pull it its own way or hold it with
// every layer built on it. A dead download server is asked once; a tarball the
// server does not have (404) is one file, and the next router's is asked.
func TestResolveBaseImagesKeepsTheImageWhenTheTarballDoesNotAnswerEither(t *testing.T) {
	host, _ := fakeRegistry(t, func(string) int { return http.StatusServiceUnavailable })
	body := `
version: 1
routers:
  - id: a
    release: "25.12.5"
    arch: x86_64
    image: ` + host + `/openwrt/rootfs:x86_64-25.12.5
  - id: b
    release: "24.10.8"
    arch: x86_64
    image: ` + host + `/openwrt/rootfs:x86_64-24.10.8
`
	for _, c := range []struct {
		name  string
		err   error
		asked int32
	}{
		{"server down", &netx.OutageError{Attempts: 4, Err: errors.New("dial tcp: lookup downloads.openwrt.org: no such host")}, 1},
		{"file missing", &netx.StatusError{URL: "https://downloads.openwrt.org/x.tar.gz", Code: 404, Status: "404 Not Found"}, 2},
	} {
		asked := fakeTarballs(t, c.err)
		routers := loadRouters(t, body)
		resolveBaseImages(t.Context(), routers, false)
		for _, r := range routers {
			if r.BaseImage() != r.Image {
				t.Errorf("%s: %s: base image %q, want its own %q", c.name, r.ID, r.BaseImage(), r.Image)
			}
		}
		if asked.Load() != c.asked {
			t.Errorf("%s: the download server was asked %d times, want %d", c.name, asked.Load(), c.asked)
		}
	}
}

// `owlab context` prepares the images owlab publishes. A tag built from the
// tarball would carry linux/arm64 where upstream's says linux/aarch64_generic,
// and every router whose `image:` names it would fail to pull it, so a publish
// keeps the image through an outage and does not look at the tarball.
func TestResolveBaseImagesForAPublishKeepsTheImageThroughAnOutage(t *testing.T) {
	host, _ := fakeRegistry(t, func(string) int { return http.StatusServiceUnavailable })
	asked := fakeTarballs(t, nil)
	routers := loadRouters(t, `
version: 1
routers:
  - id: arm
    release: "25.12.5"
    arch: aarch64_generic
    image: `+host+`/openwrt/rootfs:aarch64_generic-25.12.5
`)
	resolveBaseImages(t.Context(), routers, true)
	if r := routers[0]; r.BaseImage() != r.Image || r.Platform() != "linux/aarch64_generic" {
		t.Errorf("base image %q on %s, want %q on linux/aarch64_generic", r.BaseImage(), r.Platform(), r.Image)
	}
	if asked.Load() != 0 {
		t.Errorf("a publish asked the download server %d times", asked.Load())
	}
}
