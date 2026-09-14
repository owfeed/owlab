package netx

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestTransient(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"404", &StatusError{Code: 404}, false},
		{"502", fmt.Errorf("sums: %w", &StatusError{Code: 502}), true},
		{"429", &StatusError{Code: 429}, true},
		{"dns", &url.Error{Op: "Get", URL: "https://x", Err: &net.DNSError{Err: "no such host", Name: "x"}}, true},
		{"unexpected EOF", fmt.Errorf("copy: %w", io.ErrUnexpectedEOF), true},
		{"canceled", context.Canceled, false},
		{"unknown authority", &url.Error{Op: "Get", URL: "https://x", Err: x509.UnknownAuthorityError{}}, false},
		{"checksum mismatch", errors.New("checksum mismatch for x"), false},
		{"transport outage", &OutageError{Attempts: 4, Err: errors.New("refused")}, true},
	}
	for _, c := range cases {
		if got := Transient(c.err); got != c.want {
			t.Errorf("%s: Transient(%v) = %v, want %v", c.name, c.err, got, c.want)
		}
	}
}

func server(t *testing.T, codes ...int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(hits.Add(1))
		code := codes[len(codes)-1]
		if n <= len(codes) {
			code = codes[n-1]
		}
		w.WriteHeader(code)
	}))
	t.Cleanup(s.Close)
	return s, &hits
}

func client() *http.Client { return &http.Client{Transport: &Transport{Delay: time.Millisecond}} }

func TestTransportRetriesUntilTheServerAnswers(t *testing.T) {
	s, hits := server(t, 502, 503, 200)
	resp, err := client().Get(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || hits.Load() != 3 {
		t.Fatalf("status %d after %d requests, want 200 after 3", resp.StatusCode, hits.Load())
	}
}

func TestTransportAsksForA404Once(t *testing.T) {
	s, hits := server(t, 404)
	resp, err := client().Get(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 || hits.Load() != 1 {
		t.Fatalf("status %d after %d requests, want 404 after 1", resp.StatusCode, hits.Load())
	}
}

func TestTransportSaysAPersistentFailureIsAnOutage(t *testing.T) {
	s, hits := server(t, 503)
	resp, err := client().Get(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if int(hits.Load()) != DefaultAttempts {
		t.Fatalf("%d requests, want %d", hits.Load(), DefaultAttempts)
	}
	if msg := Status(s.URL, resp).Error(); !strings.Contains(msg, "run the command again later") {
		t.Fatalf("a persistent 503 should say it is an outage: %s", msg)
	}

	closed := httptest.NewServer(http.NotFoundHandler())
	addr := closed.URL
	closed.Close()
	_, err = client().Get(addr)
	var oe *OutageError
	if !errors.As(err, &oe) || !strings.Contains(err.Error(), "not a problem with the config") {
		t.Fatalf("an unreachable host should be an OutageError that says so, got %v", err)
	}
}

func TestTransportDoesNotReplayAWrite(t *testing.T) {
	s, hits := server(t, 503)
	resp, err := client().Post(s.URL, "text/plain", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if hits.Load() != 1 {
		t.Fatalf("POST sent %d times, want 1", hits.Load())
	}
}
