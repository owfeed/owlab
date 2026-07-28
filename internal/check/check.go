// Package check runs assertions against a running router.
//
// This is the half of a CI run that is not "did it start". A package that
// installs cleanly and still leaves LuCI serving a stack trace is the failure
// worth catching, and the only way to catch it is to ask the router the same
// questions a person would: does the page render, is the service up, did the
// uci-defaults run.
//
// Assertions are written as one short line each, because they live in a YAML
// scalar in somebody's workflow file and anything that needs quoting inside
// quoting will be got wrong.
package check

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Exec runs a shell script on a router and writes what it printed to out.
//
// The same signature the sync package uses, and for the same reason: it is the
// seam between the two tiers. An assertion does not know whether it is being
// answered over `docker exec` or over ssh to a VM, so every kind here works on
// both without a second implementation.
type Exec func(ctx context.Context, script string, stdin []byte, out io.Writer) error

// Kinds of assertion, in the order they appear in the documentation.
const (
	KindHTTP    = "http"
	KindService = "service"
	KindFile    = "file"
	KindPackage = "package"
	KindUCI     = "uci"
	KindExec    = "exec"
)

// Check is one parsed assertion.
type Check struct {
	// Raw is the line as written, and is what gets reported.
	Raw  string
	Kind string

	// Status and Path are set for KindHTTP.
	Status statusMatcher
	Path   string
	// Arg is the single operand of every other kind.
	Arg string
}

// Result is the outcome of one assertion.
type Result struct {
	Check   string  `json:"check"`
	Kind    string  `json:"kind"`
	OK      bool    `json:"ok"`
	Detail  string  `json:"detail,omitempty"`
	Seconds float64 `json:"seconds"`
}

// statusMatcher accepts an exact code ("200") or a class ("2xx").
type statusMatcher struct {
	code  int
	class int // 2 for 2xx, 0 when an exact code is wanted
}

func (m statusMatcher) match(code int) bool {
	if m.class != 0 {
		return code/100 == m.class
	}
	return code == m.code
}

func (m statusMatcher) String() string {
	if m.class != 0 {
		return fmt.Sprintf("%dxx", m.class)
	}
	return strconv.Itoa(m.code)
}

// ParseAll parses every assertion, reporting all the bad ones at once.
//
// All of them, because these arrive from a workflow file that takes minutes to
// re-run: being told about the second typo only after fixing the first is the
// difference between one CI round trip and three.
func ParseAll(lines []string) ([]Check, error) {
	var out []Check
	var bad []string
	for _, l := range lines {
		c, err := Parse(l)
		if err != nil {
			bad = append(bad, "  "+err.Error())
			continue
		}
		out = append(out, c)
	}
	if len(bad) > 0 {
		return nil, fmt.Errorf("bad assertions:\n%s\n\n%s", strings.Join(bad, "\n"), Syntax)
	}
	return out, nil
}

// Syntax is the assertion reference, printed with every parse error.
const Syntax = `assertion syntax:
  http <status> <path>   fetch a LuCI page as root; status is 200 or 2xx
  service <name>         the init script reports it running
  file <path>            the path exists on the router
  package <name>         the package manager reports it installed
  uci <config[.sec[.opt]]>  the uci value is set
  exec <shell command>   the command exits 0`

// Parse turns one line into a Check.
func Parse(line string) (Check, error) {
	s := strings.TrimSpace(line)
	if s == "" {
		return Check{}, fmt.Errorf("empty assertion")
	}
	kind, rest, _ := strings.Cut(s, " ")
	rest = strings.TrimSpace(rest)
	c := Check{Raw: s, Kind: strings.ToLower(kind)}

	switch c.Kind {
	case KindHTTP:
		status, path, ok := strings.Cut(rest, " ")
		if !ok {
			return Check{}, fmt.Errorf("%q: http needs a status and a path, e.g. `http 200 /cgi-bin/luci/admin/status/overview`", s)
		}
		m, err := parseStatus(status)
		if err != nil {
			return Check{}, fmt.Errorf("%q: %w", s, err)
		}
		path = strings.TrimSpace(path)
		if !strings.HasPrefix(path, "/") {
			return Check{}, fmt.Errorf("%q: the path must start with / (owlab supplies the host and port)", s)
		}
		c.Status, c.Path = m, path
		return c, nil

	case KindService, KindFile, KindPackage, KindUCI, KindExec:
		if rest == "" {
			return Check{}, fmt.Errorf("%q: %s needs an argument", s, c.Kind)
		}
		c.Arg = rest
		return c, nil

	default:
		return Check{}, fmt.Errorf("%q: unknown assertion kind %q", s, kind)
	}
}

func parseStatus(s string) (statusMatcher, error) {
	if len(s) == 3 && (s[1] == 'x' || s[1] == 'X') && (s[2] == 'x' || s[2] == 'X') {
		if s[0] >= '1' && s[0] <= '5' {
			return statusMatcher{class: int(s[0] - '0')}, nil
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 100 || n > 599 {
		return statusMatcher{}, fmt.Errorf("%q is not an HTTP status or a class like 2xx", s)
	}
	return statusMatcher{code: n}, nil
}

// Target is the one router a set of checks runs against.
type Target struct {
	// Router is the id, passed back to the Runner.
	Router string
	// BaseURL is where LuCI is published, e.g. http://localhost:8080.
	BaseURL string
	// Password is root's password, normally empty.
	Password string
	// PkgManager is "apk" or "opkg"; the two answer "is this installed"
	// differently and neither understands the other's command.
	PkgManager string

	session *luci
}

// Run evaluates one assertion.
func (c Check) Run(ctx context.Context, t *Target, run Exec) Result {
	start := time.Now()
	res := Result{Check: c.Raw, Kind: c.Kind}
	var detail string
	var err error

	switch c.Kind {
	case KindHTTP:
		detail, err = c.runHTTP(ctx, t)
	default:
		detail, err = c.runShell(ctx, t, run)
	}

	res.Seconds = time.Since(start).Round(time.Millisecond).Seconds()
	if err != nil {
		res.Detail = err.Error()
		return res
	}
	res.OK = true
	res.Detail = detail
	return res
}

func (c Check) runShell(ctx context.Context, t *Target, run Exec) (string, error) {
	if run == nil {
		return "", fmt.Errorf("%s is not running", t.Router)
	}
	cmd, describe := c.shellCommand(t.PkgManager)
	var buf bytes.Buffer
	err := run(ctx, cmd, nil, &buf)
	out := strings.TrimSpace(buf.String())
	if err != nil {
		if out != "" {
			// One line of the router's own output. The full thing is in
			// `owlab logs`, and a failed assertion that pastes 200 lines into a
			// CI log buries the next failure.
			return "", fmt.Errorf("%s: %s", describe, firstLine(out))
		}
		return "", fmt.Errorf("%s", describe)
	}
	if out != "" {
		return firstLine(out), nil
	}
	return "", nil
}

// shellCommand is the command to run on the router, and what to say when it
// fails. Kept separate from running it so both halves are testable.
func (c Check) shellCommand(pkgManager string) (cmd, failure string) {
	switch c.Kind {
	case KindService:
		// Two ways of asking, because neither is universal: an init script may
		// not implement `running` at all, and a service started outside procd
		// is invisible to ubus. Either answering yes is enough.
		return fmt.Sprintf(
				`/etc/init.d/%[1]s running 2>/dev/null || `+
					`ubus call service list %[2]s 2>/dev/null | grep -q '"running": *true'`,
				shellSafe(c.Arg), shellQuote(fmt.Sprintf(`{"name":"%s"}`, c.Arg))),
			fmt.Sprintf("service %s is not running", c.Arg)

	case KindFile:
		return "[ -e " + shellQuote(c.Arg) + " ]", "no such path on the router: " + c.Arg

	case KindPackage:
		if pkgManager == "opkg" {
			return "opkg list-installed " + shellQuote(c.Arg) + " | grep -q .",
				c.Arg + " is not installed (opkg)"
		}
		return "apk info -e " + shellQuote(c.Arg) + " | grep -q .",
			c.Arg + " is not installed (apk)"

	case KindUCI:
		return "uci -q get " + shellQuote(c.Arg) + " >/dev/null", "uci " + c.Arg + " is not set"

	default: // KindExec
		return c.Arg, "command exited non-zero"
	}
}

// runHTTP fetches a page as a logged-in root would.
//
// Logging in is the point. Every page a LuCI app exists to serve lives under
// /cgi-bin/luci/admin, and an unauthenticated request there is a redirect to
// the login form — so a naive `curl -o /dev/null -w %{http_code}` reports 403
// for a perfectly healthy page, and every maintainer writing this by hand hits
// that first.
func (c Check) runHTTP(ctx context.Context, t *Target) (string, error) {
	if t.session == nil {
		t.session = &luci{base: strings.TrimSuffix(t.BaseURL, "/"), password: t.Password}
	}
	if err := t.session.ensure(ctx); err != nil {
		return "", err
	}
	code, body, err := t.session.get(ctx, c.Path)
	if err != nil {
		return "", err
	}
	if !c.Status.match(code) {
		hint := ""
		if code == 403 {
			hint = " (LuCI rejected the session — is the page under a menu root that needs more than root's ACLs?)"
		}
		return "", fmt.Errorf("got %d, wanted %s%s", code, c.Status, hint)
	}
	// A dispatcher that catches an exception still answers 200 with the trace
	// in the body, so status alone would call a broken page healthy. This is
	// the failure a maintainer most wants CI to catch and the one a status
	// check cannot see.
	if code/100 == 2 {
		if m := errorMarker(body); m != "" {
			return "", fmt.Errorf("%d, but the page is a LuCI error page (%q)", code, m)
		}
	}
	return fmt.Sprintf("%d, %d bytes", code, len(body)), nil
}

// errorMarkers are what LuCI's two dispatchers print when they catch something.
// Lua (24.10 and earlier) and ucode (25.12 and later) word it differently, and
// both do it with a 200.
var errorMarkers = []string{
	"A runtime exception was caught", // ucode dispatcher
	"stack traceback:",               // lua
	"Uncaught exception",
	"/usr/lib/lua/luci/dispatcher.lua",
}

func errorMarker(body string) string {
	for _, m := range errorMarkers {
		if strings.Contains(body, m) {
			return m
		}
	}
	return ""
}

// luci is a logged-in LuCI session.
type luci struct {
	base     string
	password string
	client   *http.Client
	loggedIn bool
}

// dial builds the client, with the cookie jar the session lives in.
func (l *luci) dial() error {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return err
	}
	l.client = &http.Client{
		Jar:     jar,
		Timeout: 30 * time.Second,
		// Never follow: the redirect after a successful login goes to a page we
		// were not asked about, and following one on a *checked* request would
		// turn "302 to the login form" into "200 login form", which is the
		// wrong answer to report.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return nil
}

func (l *luci) ensure(ctx context.Context) error {
	if l.client == nil {
		if err := l.dial(); err != nil {
			return err
		}
	}
	if l.loggedIn {
		return nil
	}

	form := url.Values{"luci_username": {"root"}, "luci_password": {l.password}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		l.base+"/cgi-bin/luci/", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := l.client.Do(req)
	if err != nil {
		return fmt.Errorf("LuCI did not answer at %s: %w", l.base, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("no LuCI at %s/cgi-bin/luci/ — is luci-light installed on this router?", l.base)
	}
	// The login URL, not the site root. LuCI scopes the session cookie to
	// /cgi-bin/luci, so a jar queried at / reports no cookies at all and a
	// perfectly good login looks like a rejected one.
	u, err := url.Parse(l.base + "/cgi-bin/luci/")
	if err != nil {
		return err
	}
	for _, ck := range l.client.Jar.Cookies(u) {
		// The cookie is sysauth on older releases and sysauth_http over plain
		// HTTP on newer ones; matching the prefix covers both without pinning a
		// name that is not part of any interface.
		if strings.HasPrefix(ck.Name, "sysauth") && ck.Value != "" {
			l.loggedIn = true
			return nil
		}
	}
	pw := "empty"
	if l.password != "" {
		pw = "the one in OWLAB_ROOT_PASSWORD"
	}
	return fmt.Errorf("LuCI refused the login as root with %s password (HTTP %d)", pw, resp.StatusCode)
}

func (l *luci) get(ctx context.Context, path string) (int, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.base+path, nil)
	if err != nil {
		return 0, "", err
	}
	resp, err := l.client.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	// Bounded: a LuCI page is tens of kilobytes, and the only reason to read
	// the body at all is to look for an error marker in it.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return 0, "", err
	}
	return resp.StatusCode, string(body), nil
}

// shellQuote wraps a value in single quotes for /bin/sh.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellSafe strips what cannot appear in a bare word. Used for the few places
// a value is interpolated into a path rather than passed as an argument.
func shellSafe(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_' || r == '.':
			return r
		}
		return -1
	}, s)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}
