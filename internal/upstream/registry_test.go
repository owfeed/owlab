package upstream

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"owfeed.org/owlab/internal/netx"
)

func TestSplitRefReadsAReferenceTheWayTheEngineDoes(t *testing.T) {
	cases := []struct{ ref, host, repo, tag string }{
		{"openwrt/rootfs:x86_64-25.12.5", "registry-1.docker.io", "openwrt/rootfs", "x86_64-25.12.5"},
		{"docker.io/openwrt/rootfs:x86_64-master", "registry-1.docker.io", "openwrt/rootfs", "x86_64-master"},
		{"ghcr.io/openwrt/rootfs:x86_64-24.10.8", "ghcr.io", "openwrt/rootfs", "x86_64-24.10.8"},
		{"alpine", "registry-1.docker.io", "library/alpine", "latest"},
		{"localhost/rootfs:dev", "localhost", "rootfs", "dev"},
		{"127.0.0.1:5000/a/b", "127.0.0.1:5000", "a/b", "latest"},
		// A digest wins over the tag written beside it.
		{"ghcr.io/a/b:tag@sha256:abc", "ghcr.io", "a/b", "sha256:abc"},
	}
	for _, c := range cases {
		host, repo, tag := splitRef(c.ref)
		if host != c.host || repo != c.repo || tag != c.tag {
			t.Errorf("splitRef(%q) = %q %q %q, want %q %q %q", c.ref, host, repo, tag, c.host, c.repo, c.tag)
		}
	}
}

// The challenges as Docker Hub and ghcr.io send them, measured 2026-10-26.
func TestParseChallenge(t *testing.T) {
	got := parseChallenge(`Bearer realm="https://auth.docker.io/token",service="registry.docker.io",scope="repository:openwrt/rootfs:pull"`)
	if got["realm"] != "https://auth.docker.io/token" || got["service"] != "registry.docker.io" || got["scope"] != "repository:openwrt/rootfs:pull" {
		t.Errorf("docker hub: %v", got)
	}
	got = parseChallenge(`Bearer realm="https://ghcr.io/token",service="ghcr.io",scope="repository:a/b:pull,push"`)
	if got["scope"] != "repository:a/b:pull,push" || got["service"] != "ghcr.io" {
		t.Errorf("a comma inside a quoted value: %v", got)
	}
	if got := parseChallenge(`Basic realm="registry"`); got != nil {
		t.Errorf("a Basic challenge is not a token realm: %v", got)
	}
}

// registry models what ProbeImage talks to: a 401 with a Bearer challenge
// until a token from /token comes back, then manifestStatus for /v2/...
type registry struct {
	srv            *httptest.Server
	manifestStatus atomic.Int32
	tokenStatus    atomic.Int32
	challenge      atomic.Value
	manifests      atomic.Int32
	scope          atomic.Value
}

func newRegistry(t *testing.T) *registry {
	t.Helper()
	reg := &registry{}
	reg.manifestStatus.Store(http.StatusOK)
	reg.tokenStatus.Store(http.StatusOK)
	reg.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			reg.scope.Store(r.URL.Query().Get("service") + " " + r.URL.Query().Get("scope"))
			if code := int(reg.tokenStatus.Load()); code != http.StatusOK {
				w.WriteHeader(code)
				return
			}
			_, _ = fmt.Fprint(w, `{"token":"t0k"}`)
			return
		}
		reg.manifests.Add(1)
		if r.Header.Get("Authorization") != "Bearer t0k" {
			challenge, _ := reg.challenge.Load().(string)
			if challenge == "" {
				challenge = fmt.Sprintf(`Bearer realm="%s/token",service="test-registry"`, reg.srv.URL)
			}
			w.Header().Set("WWW-Authenticate", challenge)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(int(reg.manifestStatus.Load()))
	}))
	t.Cleanup(reg.srv.Close)

	oldTransport, oldDelay := RegistryTransport, netx.DefaultDelay
	RegistryTransport, netx.DefaultDelay = reg.srv.Client().Transport, time.Millisecond
	t.Cleanup(func() { RegistryTransport, netx.DefaultDelay = oldTransport, oldDelay })
	return reg
}

func (reg *registry) ref(tag string) string {
	return strings.TrimPrefix(reg.srv.URL, "https://") + "/openwrt/rootfs:" + tag
}

// The healthy direction: a published image is found, with the anonymous token
// asked for this repository's pull scope.
func TestProbeImageFindsAPublishedImage(t *testing.T) {
	reg := newRegistry(t)
	state, err := ProbeImage(t.Context(), reg.ref("x86_64-25.12.5"))
	if state != ImageFound || err != nil {
		t.Fatalf("got %v, %v; want ImageFound", state, err)
	}
	if got, _ := reg.scope.Load().(string); got != "test-registry repository:openwrt/rootfs:pull" {
		t.Errorf("token asked for %q", got)
	}
}

func TestProbeImageTellsAMissingTagFromAnOutage(t *testing.T) {
	reg := newRegistry(t)
	reg.manifestStatus.Store(http.StatusNotFound)
	state, err := ProbeImage(t.Context(), reg.ref("x86_64-99.0.0"))
	if state != ImageMissing || err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("a 404: got %v, %v; want ImageMissing", state, err)
	}

	reg.manifestStatus.Store(http.StatusServiceUnavailable)
	state, err = ProbeImage(t.Context(), reg.ref("x86_64-25.12.5"))
	if state != RegistryDown || err == nil || !strings.Contains(err.Error(), "503") {
		t.Errorf("a registry answering 503: got %v, %v; want RegistryDown", state, err)
	}
}

func TestProbeImageCallsAFailingTokenServiceAnOutage(t *testing.T) {
	reg := newRegistry(t)
	reg.tokenStatus.Store(http.StatusBadGateway)
	if state, err := ProbeImage(t.Context(), reg.ref("x86_64-25.12.5")); state != RegistryDown {
		t.Errorf("got %v, %v; want RegistryDown", state, err)
	}
}

func TestProbeImageCallsARegistryThatRefusesConnectionsAnOutage(t *testing.T) {
	reg := newRegistry(t)
	ref := reg.ref("x86_64-25.12.5")
	reg.srv.Close()
	state, err := ProbeImage(t.Context(), ref)
	if state != RegistryDown || err == nil || !strings.Contains(err.Error(), "no answer after 4 attempts") {
		t.Errorf("got %v, %v; want RegistryDown after netx's attempts", state, err)
	}
}

// A registry that wants real credentials, or will not hand this image to an
// anonymous token, is an answer only the engine can settle: it may hold the
// credentials. Neither is an outage, so neither moves the router off its image.
func TestProbeImageLeavesAPrivateImageToTheEngine(t *testing.T) {
	reg := newRegistry(t)
	reg.challenge.Store(`Basic realm="private"`)
	if state, err := ProbeImage(t.Context(), reg.ref("x86_64-25.12.5")); state != ImageFound || err == nil {
		t.Errorf("a Basic challenge: got %v, %v; want ImageFound with the reason", state, err)
	}

	reg.challenge.Store("")
	reg.tokenStatus.Store(http.StatusForbidden)
	if state, _ := ProbeImage(t.Context(), reg.ref("x86_64-25.12.5")); state != ImageFound {
		t.Errorf("a token refused with 403: got %v; want ImageFound", state)
	}
}

// resolveBaseImages falls back to a tarball only when HeadFile says the server
// has it, and records the server as down only when its error is an outage.
func TestHeadFileTellsAFileFromAMissingOneFromAnOutage(t *testing.T) {
	old := netx.DefaultDelay
	netx.DefaultDelay = time.Millisecond
	t.Cleanup(func() { netx.DefaultDelay = old })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rootfs.tar.gz":
		case "/failing.tar.gz":
			w.WriteHeader(http.StatusBadGateway)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	if err := HeadFile(t.Context(), srv.URL+"/rootfs.tar.gz"); err != nil {
		t.Errorf("a file the server has: %v", err)
	}
	if err := HeadFile(t.Context(), srv.URL+"/missing.tar.gz"); err == nil || netx.Transient(err) || !strings.Contains(err.Error(), "404") {
		t.Errorf("a 404 is an answer, not an outage: %v", err)
	}
	if err := HeadFile(t.Context(), srv.URL+"/failing.tar.gz"); err == nil || !netx.Transient(err) {
		t.Errorf("a 502 through netx's attempts is an outage: %v", err)
	}
}
