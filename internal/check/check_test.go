package check

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	ok := []struct {
		in   string
		kind string
	}{
		{"http 200 /cgi-bin/luci/admin/services/x", KindHTTP},
		{"http 2xx /", KindHTTP},
		{"  service  dnsmasq  ", KindService},
		{"file /etc/config/mine", KindFile},
		{"package luci-app-mine", KindPackage},
		{"uci mine.@mine[0].enabled", KindUCI},
		{"exec pgrep -f mined && echo yes", KindExec},
	}
	for _, tc := range ok {
		c, err := Parse(tc.in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.in, err)
		}
		if c.Kind != tc.kind {
			t.Errorf("Parse(%q) kind = %q, want %q", tc.in, c.Kind, tc.kind)
		}
	}

	bad := []string{
		"",
		"http",
		"http 200",
		"http 999 /x",
		"http 200 cgi-bin/luci", // no leading slash
		"htp 200 /x",            // typo in the kind
		"service",               // no argument
	}
	for _, in := range bad {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) succeeded, want an error", in)
		}
	}
}

// A workflow file is slow to iterate on, so every bad line has to be reported
// in one go rather than one per CI round trip.
func TestParseAllReportsEveryBadLine(t *testing.T) {
	_, err := ParseAll([]string{"http 200 /ok", "nonsense here", "http 200 nopath"})
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"nonsense here", "nopath"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q:\n%s", want, err)
		}
	}
}

func TestStatusMatcher(t *testing.T) {
	m, err := parseStatus("2xx")
	if err != nil {
		t.Fatal(err)
	}
	if !m.match(204) || m.match(301) {
		t.Errorf("2xx matched wrongly")
	}
	m, _ = parseStatus("404")
	if !m.match(404) || m.match(400) {
		t.Errorf("404 matched wrongly")
	}
}

// The two package managers do not understand each other's commands, so the
// wrong one here reports every package as missing.
func TestShellCommandPerPackageManager(t *testing.T) {
	c, _ := Parse("package luci-app-mine")
	apk, _ := c.shellCommand("apk")
	opkg, _ := c.shellCommand("opkg")
	if !strings.HasPrefix(apk, "apk info -e") {
		t.Errorf("apk command = %q", apk)
	}
	if !strings.HasPrefix(opkg, "opkg list-installed") {
		t.Errorf("opkg command = %q", opkg)
	}
}

func TestShellQuoting(t *testing.T) {
	c, _ := Parse("file /etc/it's there")
	cmd, _ := c.shellCommand("apk")
	if strings.Contains(cmd, "it's there") {
		t.Errorf("apostrophe left unquoted: %s", cmd)
	}
	if want := `[ -e '/etc/it'\''s there' ]`; cmd != want {
		t.Errorf("cmd = %q, want %q", cmd, want)
	}
}

// fakeExec answers like a router that printed `out` and exited with `err`.
type fakeExec struct {
	out  string
	err  error
	last string
}

func (f *fakeExec) run(_ context.Context, script string, _ []byte, w io.Writer) error {
	f.last = script
	fmt.Fprint(w, f.out)
	return f.err
}

func TestRunShell(t *testing.T) {
	c, _ := Parse("service dnsmasq")
	tgt := &Target{Router: "r", PkgManager: "apk"}

	f := &fakeExec{}
	if res := c.Run(context.Background(), tgt, f.run); !res.OK {
		t.Errorf("want pass, got %+v", res)
	}

	f = &fakeExec{err: errors.New("exit status 1")}
	res := c.Run(context.Background(), tgt, f.run)
	if res.OK {
		t.Fatal("want failure")
	}
	if !strings.Contains(res.Detail, "not running") {
		t.Errorf("detail = %q", res.Detail)
	}
}

// The router's own output can be long; a CI log that buries the next failure
// under 200 lines of it is worse than one that says where to look.
func TestRunShellTrimsOutput(t *testing.T) {
	c, _ := Parse("exec false")
	f := &fakeExec{out: "line one\nline two\nline three", err: errors.New("exit status 1")}
	res := c.Run(context.Background(), &Target{Router: "r"}, f.run)
	if strings.Contains(res.Detail, "line two") {
		t.Errorf("detail kept more than the first line: %q", res.Detail)
	}
}

// luciServer is a stand-in for a router: it sets a sysauth cookie on a login
// POST and refuses everything else without it.
func luciServer(t *testing.T, page func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/cgi-bin/luci/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if err := r.ParseForm(); err != nil {
				http.Error(w, "bad form", http.StatusBadRequest)
				return
			}
			if r.PostForm.Get("luci_username") != "root" {
				http.Error(w, "no", http.StatusForbidden)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "sysauth_http", Value: "deadbeef", Path: "/"})
			w.Header().Set("Location", "/cgi-bin/luci/admin/status/overview")
			w.WriteHeader(http.StatusFound)
			return
		}
		if _, err := r.Cookie("sysauth_http"); err != nil {
			w.Header().Set("Location", "/cgi-bin/luci/")
			w.WriteHeader(http.StatusFound)
			return
		}
		page(w, r)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestHTTPCheckLogsIn(t *testing.T) {
	srv := luciServer(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "<html>Interfaces</html>")
	})
	c, _ := Parse("http 200 /cgi-bin/luci/admin/network/network")
	res := c.Run(context.Background(), &Target{BaseURL: srv.URL}, nil)
	if !res.OK {
		t.Fatalf("want pass, got %q", res.Detail)
	}
}

// The whole reason the session exists: without the cookie every admin page is
// a 302 to the login form, so a status-only check reports a healthy app as
// broken. Fetching the same page with no session must not pass.
func TestHTTPCheckWithoutSessionIsARedirect(t *testing.T) {
	srv := luciServer(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "ok")
	})
	l := &luci{base: srv.URL}
	l.loggedIn = true // pretend, so no cookie is ever obtained
	if err := l.ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	code, _, err := l.get(context.Background(), "/cgi-bin/luci/admin/network/network")
	if err != nil {
		t.Fatal(err)
	}
	if code != http.StatusFound {
		t.Fatalf("unauthenticated GET = %d, want 302 — the fixture is not modelling LuCI", code)
	}
}

// A login that never yields a session cookie has to say so, not fail later as
// a mysterious 403 on the page being checked.
func TestLoginFailureIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "<html>login form</html>")
	}))
	t.Cleanup(srv.Close)
	c, _ := Parse("http 200 /cgi-bin/luci/admin/status/overview")
	res := c.Run(context.Background(), &Target{BaseURL: srv.URL}, nil)
	if res.OK {
		t.Fatal("want failure")
	}
	if !strings.Contains(res.Detail, "refused the login") {
		t.Errorf("detail = %q", res.Detail)
	}
}

func TestHTTPCheckStatusMismatch(t *testing.T) {
	srv := luciServer(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	})
	c, _ := Parse("http 200 /cgi-bin/luci/admin/services/mine")
	res := c.Run(context.Background(), &Target{BaseURL: srv.URL}, nil)
	if res.OK {
		t.Fatal("want failure")
	}
	if !strings.Contains(res.Detail, "got 404") {
		t.Errorf("detail = %q", res.Detail)
	}
}

// A caught exception is served with a 200. Status alone calls that page
// healthy, which is the exact bug this is here to catch.
func TestHTTPCheckCatchesErrorPageWith200(t *testing.T) {
	srv := luciServer(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "<html><pre>A runtime exception was caught: reference error</pre></html>")
	})
	c, _ := Parse("http 200 /cgi-bin/luci/admin/services/mine")
	res := c.Run(context.Background(), &Target{BaseURL: srv.URL}, nil)
	if res.OK {
		t.Fatal("want failure on a LuCI error page served with 200")
	}
	if !strings.Contains(res.Detail, "error page") {
		t.Errorf("detail = %q", res.Detail)
	}
}

func TestHTTPCheckNoLuCI(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	c, _ := Parse("http 200 /cgi-bin/luci/")
	res := c.Run(context.Background(), &Target{BaseURL: srv.URL}, nil)
	if res.OK {
		t.Fatal("want failure")
	}
	if !strings.Contains(res.Detail, "luci-light") {
		t.Errorf("detail should say what is missing, got %q", res.Detail)
	}
}
