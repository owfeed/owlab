// Package netx tells a download server that is failing from one that answered, and
// retries the first.
//
// owlab fetches a rootfs, a VM image and its sha256sums, out-of-feed packages and the
// release listings from servers it does not run. Before this package one 502 from a
// mirror ended the command with the same kind of message as a release that does not
// exist, and nothing was asked twice. Now a 5xx, a 429 or a dropped connection is
// retried a few times, and what still fails says whether it was an outage (run again
// later) or an answer (fix the release or URL).
//
// owfeed has a package of the same name and shape. It is a copy on purpose: the two
// tools share no Go module (docs/ECOSYSTEM.md in owfeed).
package netx

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"syscall"
	"time"
)

// StatusError is a response that was not 200.
type StatusError struct {
	URL    string
	Code   int
	Status string
}

func (e *StatusError) Error() string {
	msg := fmt.Sprintf("GET %s: %s", e.URL, e.Status)
	if TransientStatus(e.Code) {
		msg += " (the server is failing, not refusing: owlab retried; run the command again later)"
	}
	return msg
}

// Status builds the error for a response that was not 200.
func Status(url string, resp *http.Response) error {
	return &StatusError{URL: url, Code: resp.StatusCode, Status: resp.Status}
}

// TransientStatus reports whether an HTTP status is one a server returns while it is
// failing rather than while it is answering: 408, 425, 429 and every 5xx.
func TransientStatus(code int) bool {
	return code == http.StatusRequestTimeout || code == http.StatusTooEarly ||
		code == http.StatusTooManyRequests || code >= 500
}

// Transient reports whether err is an outage rather than an answer. Unknown errors
// are not transient.
func Transient(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var oe *OutageError
	if errors.As(err, &oe) {
		return true
	}
	var se *StatusError
	if errors.As(err, &se) {
		return TransientStatus(se.Code)
	}
	// A certificate that does not verify does not heal by asking again, and it
	// arrives wrapped in the same *url.Error as the network cases below.
	var certErr *tls.CertificateVerificationError
	var unknownAuth x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	var invalid x509.CertificateInvalidError
	if errors.As(err, &certErr) || errors.As(err, &unknownAuth) ||
		errors.As(err, &hostErr) || errors.As(err, &invalid) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	var dnsErr *net.DNSError
	var opErr *net.OpError
	if errors.As(err, &dnsErr) || errors.As(err, &opErr) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// Four attempts, 2 s, 4 s and 8 s apart. Variables so tests keep the attempts and
// drop the waiting.
var (
	DefaultAttempts = 4
	DefaultDelay    = 2 * time.Second
)

// OutageError is what Transport returns when a transient transport failure outlasted
// every attempt.
type OutageError struct {
	Attempts int
	Err      error
}

func (e *OutageError) Error() string {
	return fmt.Sprintf("unreachable after %d attempts (a network or server outage, not a problem with the config; run the command again later): %v", e.Attempts, e.Err)
}
func (e *OutageError) Unwrap() error { return e.Err }

// Transport retries GET and HEAD requests whose failure is Transient, with a delay
// that doubles. Requests with a body pass through untouched. Only the response
// headers are covered: a body that breaks off halfway is the caller's read error.
type Transport struct {
	Base     http.RoundTripper
	Attempts int
	Delay    time.Duration
}

func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	if (req.Method != http.MethodGet && req.Method != http.MethodHead) ||
		(req.Body != nil && req.Body != http.NoBody) {
		return base.RoundTrip(req)
	}
	attempts, delay := t.Attempts, t.Delay
	if attempts <= 0 {
		attempts = DefaultAttempts
	}
	if delay <= 0 {
		delay = DefaultDelay
	}

	for attempt := 1; ; attempt++ {
		resp, err := base.RoundTrip(req)
		retry := false
		switch {
		case err != nil:
			retry = Transient(err)
		case TransientStatus(resp.StatusCode):
			retry = true
		}
		if !retry {
			return resp, err
		}
		if attempt >= attempts {
			if err != nil {
				return nil, &OutageError{Attempts: attempt, Err: err}
			}
			return resp, nil
		}
		if resp != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
			resp.Body.Close()
		}
		timer := time.NewTimer(delay)
		select {
		case <-req.Context().Done():
			timer.Stop()
			return nil, req.Context().Err()
		case <-timer.C:
		}
		delay *= 2
	}
}

// Client returns a copy of hc whose transport retries outages. hc is not modified.
func Client(hc *http.Client) *http.Client {
	if hc == nil {
		hc = http.DefaultClient
	}
	c := *hc
	c.Transport = &Transport{Base: hc.Transport}
	return &c
}
