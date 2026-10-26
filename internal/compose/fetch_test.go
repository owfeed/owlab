package compose

import (
	"errors"
	"fmt"
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
)

// quickNet keeps netx's attempts and Fetch's stall deadline, and drops the
// waiting.
func quickNet(t *testing.T, stall time.Duration) {
	t.Helper()
	oldDelay, oldStall := netx.DefaultDelay, StallAfter
	netx.DefaultDelay, StallAfter = time.Millisecond, stall
	t.Cleanup(func() { netx.DefaultDelay, StallAfter = oldDelay, oldStall })
}

// hang sends what it has and then goes quiet until the client gives up, which is
// what a CDN node that stops mid-file looks like from the client.
func hang(w http.ResponseWriter, r *http.Request, sent string) {
	w.Header().Set("Content-Length", "10")
	_, _ = w.Write([]byte(sent))
	w.(http.Flusher).Flush()
	<-r.Context().Done()
}

func TestFetchAsksAgainWhenTheBodyStalls(t *testing.T) {
	quickNet(t, 200*time.Millisecond)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			hang(w, r, "01234")
			return
		}
		_, _ = w.Write([]byte("0123456789"))
	}))
	defer srv.Close()

	got, err := Fetch(srv.URL+"/pkg.apk", t.TempDir())
	if err != nil {
		t.Fatalf("a body that stalled once should be asked for again: %v", err)
	}
	if body, _ := os.ReadFile(got); string(body) != "0123456789" {
		t.Errorf("cached %q, want the whole second body", body)
	}
	if hits.Load() != 2 {
		t.Errorf("%d requests, want 2", hits.Load())
	}
}

func TestFetchAsksAgainWhenTheBodyBreaksOff(t *testing.T) {
	quickNet(t, time.Minute)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			// Five of the ten bytes promised, then the connection drops.
			w.Header().Set("Content-Length", "10")
			_, _ = w.Write([]byte("01234"))
			w.(http.Flusher).Flush()
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				conn.Close()
			}
			return
		}
		_, _ = w.Write([]byte("0123456789"))
	}))
	defer srv.Close()

	if _, err := Fetch(srv.URL+"/pkg.apk", t.TempDir()); err != nil {
		t.Fatalf("a body that broke off once should be asked for again: %v", err)
	}
	if hits.Load() != 2 {
		t.Errorf("%d requests, want 2", hits.Load())
	}
}

// A server that keeps going quiet is an outage, reported as one after
// netx.DefaultAttempts, with no partial file left to pass for a complete one.
func TestFetchReportsAServerThatKeepsStallingAsAnOutage(t *testing.T) {
	quickNet(t, 100*time.Millisecond)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		hang(w, r, "01234")
	}))
	defer srv.Close()

	cache := t.TempDir()
	_, err := Fetch(srv.URL+"/pkg.apk", cache)
	var outage *netx.OutageError
	if !errors.As(err, &outage) {
		t.Fatalf("want an outage, got %v", err)
	}
	if !strings.Contains(err.Error(), "sent nothing for") || !strings.Contains(err.Error(), "/pkg.apk") {
		t.Errorf("the message should say the server went quiet, and on what: %v", err)
	}
	if int(hits.Load()) != netx.DefaultAttempts {
		t.Errorf("%d requests, want %d", hits.Load(), netx.DefaultAttempts)
	}
	if left, _ := filepath.Glob(filepath.Join(cache, "*")); len(left) != 0 {
		t.Errorf("a failed download left %v in the cache", left)
	}
}

// The deadline is on silence, not on the whole download: a slow link that keeps
// delivering finishes, however long it takes, and is asked once.
func TestFetchLetsASlowDownloadThatKeepsSendingFinish(t *testing.T) {
	quickNet(t, 300*time.Millisecond)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Length", "10")
		// One byte every 100 ms: a second in all, more than three times the
		// stall deadline, and never a third of it without a byte.
		for i := range 10 {
			_, _ = fmt.Fprintf(w, "%d", i)
			w.(http.Flusher).Flush()
			time.Sleep(100 * time.Millisecond)
		}
	}))
	defer srv.Close()

	got, err := Fetch(srv.URL+"/pkg.apk", t.TempDir())
	if err != nil {
		t.Fatalf("a download that keeps sending was cut: %v", err)
	}
	if body, _ := os.ReadFile(got); string(body) != "0123456789" {
		t.Errorf("cached %q", body)
	}
	if hits.Load() != 1 {
		t.Errorf("%d requests, want 1", hits.Load())
	}
}

// netx already asked a failing server netx.DefaultAttempts times before it
// answered with a status. Fetch must not multiply that, and a 404 is still
// asked once.
func TestFetchDoesNotRetryAStatusAgain(t *testing.T) {
	quickNet(t, time.Minute)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if strings.HasSuffix(r.URL.Path, "/missing.apk") {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	if _, err := Fetch(srv.URL+"/missing.apk", t.TempDir()); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("want the 404, got %v", err)
	}
	if hits.Load() != 1 {
		t.Errorf("a 404 was asked %d times, want 1", hits.Load())
	}

	hits.Store(0)
	if _, err := Fetch(srv.URL+"/pkg.apk", t.TempDir()); err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("want the 502, got %v", err)
	}
	if int(hits.Load()) != netx.DefaultAttempts {
		t.Errorf("a 502 was asked %d times, want netx's %d", hits.Load(), netx.DefaultAttempts)
	}
}

// seedCompliance puts a router's compliance files in the cache, so that
// Materialize does not reach the real download server from a unit test.
func seedCompliance(t *testing.T, r *config.Router, cacheDir string) {
	t.Helper()
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range complianceFiles(r) {
		if err := os.WriteFile(cachePath(r.ReleaseDir()+"/"+name, cacheDir), []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// `owlab up owrt2512` builds one router. A package that only the 24.10 router
// installs used to be downloaded as well, and a 5xx on it failed the 25.12 job.
func TestMaterializeFetchesOnlyTheRoutersBeingBuilt(t *testing.T) {
	quickNet(t, time.Minute)
	var bad atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".ipk") {
			bad.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte("apk"))
	}))
	defer srv.Close()

	cfg := load(t, fmt.Sprintf(`
version: 1
project:
  name: demo
routers:
  - id: new
    release: "25.12.4"
    arch: x86_64
    extra_packages:
      - {name: pkg, apk: %[1]s/pkg.apk, ipk: %[1]s/pkg.ipk}
  - id: bare
    release: "25.12.4"
    arch: x86_64
  - id: old
    release: "24.10.8"
    arch: x86_64
    extra_packages:
      - {name: pkg, apk: %[1]s/pkg.apk, ipk: %[1]s/pkg.ipk}
`, srv.URL))
	newR, _ := cfg.Router("new")
	bareR, _ := cfg.Router("bare")
	oldR, _ := cfg.Router("old")
	cacheDir := filepath.Join(cfg.Dir, WorkDirName, "cache")
	seedCompliance(t, newR, cacheDir)
	seedCompliance(t, oldR, cacheDir)

	p, err := Render(cfg, engineInfoForTest())
	if err != nil {
		t.Fatal(err)
	}
	if err := Materialize(cfg, p, []*config.Router{newR, bareR}); err != nil {
		t.Fatalf("building new and bare failed on a package only old installs: %v", err)
	}
	if bad.Load() != 0 {
		t.Errorf("old's .ipk was asked for %d times by a build that does not include old", bad.Load())
	}
	for _, want := range []string{
		"extra/new/00-pkg.apk",
		// Present though empty: the Dockerfile copies this router's directory
		// unconditionally.
		"extra/bare",
		"compliance/new/README",
		"compliance/bare/README",
	} {
		if _, err := os.Stat(filepath.Join(p.ContextDir, filepath.FromSlash(want))); err != nil {
			t.Errorf("%s was not staged: %v", want, err)
		}
	}
	for _, unwanted := range []string{"extra/old", "compliance/old"} {
		if _, err := os.Stat(filepath.Join(p.ContextDir, filepath.FromSlash(unwanted))); err == nil {
			t.Errorf("%s was staged for a router that is not being built", unwanted)
		}
	}

	// The other direction: building the router that does install it still
	// fails, and says which router and package.
	err = Materialize(cfg, p, []*config.Router{oldR})
	if err == nil || !strings.Contains(err.Error(), "old: pkg:") || !strings.Contains(err.Error(), "500") {
		t.Fatalf("want old's package failure, got %v", err)
	}
}
