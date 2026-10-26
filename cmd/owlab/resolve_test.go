package main

import (
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
	resolveBaseImages(t.Context(), routers)
	for _, r := range routers {
		if r.BaseImage() != r.Image {
			t.Errorf("%s: base image %q, want its own %q", r.ID, r.BaseImage(), r.Image)
		}
	}
}

// A registry that does not answer moves every router on it to the tarball,
// with the honest platform a tarball build carries, and is asked once.
func TestResolveBaseImagesBuildsFromTheTarballWhenTheRegistryIsDown(t *testing.T) {
	host, hits := fakeRegistry(t, func(string) int { return http.StatusServiceUnavailable })
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
	resolveBaseImages(t.Context(), routers)
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
