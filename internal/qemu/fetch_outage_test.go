package qemu

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"owfeed.org/owlab/internal/netx"
)

// A mirror that answers 502 once and then serves the image. Before netx the first 502
// ended the download; now it is asked again, and a 404 still is not.
func TestFetchToRetriesAFailingMirrorButNotAMissingImage(t *testing.T) {
	old := netx.DefaultDelay
	netx.DefaultDelay = time.Millisecond
	defer func() { netx.DefaultDelay = old }()

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		switch {
		case strings.HasSuffix(r.URL.Path, "/missing.img.gz"):
			http.NotFound(w, r)
		case n == 1:
			w.WriteHeader(http.StatusBadGateway)
		default:
			_, _ = w.Write([]byte("image"))
		}
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "img.gz")
	if _, err := fetchTo(context.Background(), srv.URL+"/openwrt.img.gz", dest); err != nil {
		t.Fatalf("a single 502 should be retried: %v", err)
	}
	if hits.Load() != 2 {
		t.Fatalf("%d requests, want 2", hits.Load())
	}

	hits.Store(10) // past the 502
	before := hits.Load()
	_, err := fetchTo(context.Background(), srv.URL+"/missing.img.gz", dest)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("want the not-found message, got %v", err)
	}
	if hits.Load()-before != 1 {
		t.Fatalf("a 404 was asked %d times, want 1", hits.Load()-before)
	}
}
