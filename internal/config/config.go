// Package config parses owlab.yaml — the one file a developer writes to
// describe the routers they want.
//
// The file is declarative and per-project: it lives next to the LuCI package
// being developed, names the releases to test against, and says how realistic
// each of them has to be. Everything owlab does is derived from it.
package config

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// FileName is the config file owlab looks for, walking up from the
// working directory.
const FileName = "owlab.yaml"

// Config is a parsed, merged and validated owlab.yaml.
type Config struct {
	Version int      `yaml:"version"`
	Project Project  `yaml:"project"`
	Routers []Router `yaml:"-"`

	// Dir is the directory the config was found in. All project-relative
	// paths resolve against it, so owlab works from any subdirectory.
	Dir string `yaml:"-"`
}

// Project describes the package under development.
type Project struct {
	// Name defaults to the config directory's base name.
	Name string `yaml:"name"`

	// Install maps source directories in the repo to destinations in the
	// router. The defaults mirror luci.mk's Package/<name>/install, so a
	// standard LuCI package needs no install: block at all.
	Install map[string]string `yaml:"install"`

	// Theme, when set, is the theme to activate after a sync. Themes need
	// this because LuCI resolves templates through luci.main.mediaurlbase,
	// and a theme whose templates are not where mediaurlbase points crashes
	// the dispatcher rather than falling back.
	Theme string `yaml:"theme"`
}

// Ports are the host ports forwarded to a router.
//
// Published ports are the only portable way to reach a container: bridge
// subnets are routable from the host on native Linux and inside WSL2, but not
// on Docker Desktop for Mac or Windows. Nothing in owlab may address a
// router by its bridge IP.
type Ports struct {
	HTTP int `yaml:"http"`
	SSH  int `yaml:"ssh"`
}

// ExtraPackage is a package installed from a URL rather than from a feed.
//
// This is how a developer gets their own package — or anything else that
// lives on GitHub Releases rather than in OpenWrt's feeds — onto the router.
// It needs two URLs because the two package managers do not share a file
// naming scheme: the same release publishes
// luci-theme-footstrap-0.11.5-r1.apk and
// luci-theme-footstrap_0.11.5-r1_all.ipk.
type ExtraPackage struct {
	// Name is for messages and for the downloaded file; defaults to the
	// basename of whichever URL is used.
	Name string `yaml:"name"`
	// APK is used on releases that ship apk (25.12 and later).
	APK string `yaml:"apk"`
	// IPK is used on releases that ship opkg (24.10 and earlier).
	IPK string `yaml:"ipk"`
}

// URLFor returns the download URL for a package manager, or "" when this
// package has nothing for it.
func (e ExtraPackage) URLFor(pm PackageManager) string {
	if pm == APK {
		return e.APK
	}
	return e.IPK
}

// Router is one emulated router, after defaults have been merged in.
type Router struct {
	ID       string
	Distro   Distro
	Release  string
	Arch     string
	Fidelity Fidelity
	Packages []string
	Extra    []ExtraPackage
	Fixtures []string
	Ports    Ports
	Hostname string

	target Target
}

// Target is the resolved build target for this router.
func (r *Router) Target() Target { return r.target }

// PackageManager is apk or opkg, per this router's release.
func (r *Router) PackageManager() PackageManager { return PackageManagerFor(r.Release) }

// FromTarball reports whether this router must be built by unpacking a rootfs
// tarball rather than by pulling an upstream image.
func (r *Router) FromTarball() bool { return r.BaseImage() == "" }

// Platform is what to pass to `docker --platform` for this router.
//
// When we build from a tarball onto scratch we control the metadata and stamp
// the honest OCI platform. When we inherit an upstream image we must repeat
// its own architecture string, standard or not, or the pull will not resolve.
func (r *Router) Platform() string {
	if r.FromTarball() {
		return r.target.HostPlatform
	}
	return r.target.OCIPlatform
}

// ---------------------------------------------------------------------------
// Raw types: what the YAML literally contains, before defaults are merged.
//
// Pointers everywhere a default can apply, so "absent" is distinguishable
// from "set to the zero value". Without that, `fidelity: basic` on a router
// would be indistinguishable from no fidelity at all, and defaults could
// never be overridden downward.
// ---------------------------------------------------------------------------

type rawConfig struct {
	Version  int         `yaml:"version"`
	Project  Project     `yaml:"project"`
	Defaults rawRouter   `yaml:"defaults"`
	Routers  []rawRouter `yaml:"routers"`
}

type rawRouter struct {
	ID       *string        `yaml:"id"`
	Distro   *string        `yaml:"distro"`
	Release  *string        `yaml:"release"`
	Arch     *string        `yaml:"arch"`
	Fidelity *string        `yaml:"fidelity"`
	Packages []string       `yaml:"packages"`
	Extra    []ExtraPackage `yaml:"extra_packages"`
	Fixtures []string       `yaml:"fixtures"`
	Ports    *Ports         `yaml:"ports"`
	Hostname *string        `yaml:"hostname"`
}

// Find walks up from dir looking for owlab.yaml.
func Find(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		p := filepath.Join(abs, FileName)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", fmt.Errorf("no %s found in %s or any parent directory", FileName, dir)
		}
		abs = parent
	}
}

// Load reads, merges and validates a config file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw rawConfig
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	cfg := &Config{
		Version: raw.Version,
		Project: raw.Project,
		Dir:     filepath.Dir(path),
	}
	if cfg.Version == 0 {
		cfg.Version = 1
	}
	if cfg.Version != 1 {
		return nil, fmt.Errorf("%s: unsupported version %d (this owlab understands version 1)", path, cfg.Version)
	}
	if cfg.Project.Name == "" {
		cfg.Project.Name = filepath.Base(cfg.Dir)
	}
	if len(cfg.Project.Install) == 0 {
		cfg.Project.Install = DefaultInstall()
	}

	if len(raw.Routers) == 0 {
		return nil, fmt.Errorf("%s: no routers defined", path)
	}
	for i, rr := range raw.Routers {
		r, err := merge(raw.Defaults, rr, i)
		if err != nil {
			return nil, fmt.Errorf("%s: routers[%d]: %w", path, i, err)
		}
		cfg.Routers = append(cfg.Routers, r)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// DefaultInstall is luci.mk's source-to-destination mapping. A standard LuCI
// package laid out the upstream way needs no configuration.
func DefaultInstall() map[string]string {
	return map[string]string{
		"htdocs": "/www",
		"ucode":  "/usr/share/ucode/luci",
		"luasrc": "/usr/lib/lua/luci",
		"root":   "/",
	}
}

// InstallPairs returns the install mapping in a stable order, so that a sync
// copies directories the same way every time.
func (c *Config) InstallPairs() [][2]string {
	keys := make([]string, 0, len(c.Project.Install))
	for k := range c.Project.Install {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([][2]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, [2]string{k, c.Project.Install[k]})
	}
	return out
}

func merge(def, r rawRouter, index int) (Router, error) {
	pick := func(a, b *string, fallback string) string {
		if b != nil {
			return *b
		}
		if a != nil {
			return *a
		}
		return fallback
	}

	out := Router{
		ID:       pick(def.ID, r.ID, ""),
		Distro:   Distro(pick(def.Distro, r.Distro, string(OpenWrt))),
		Release:  pick(def.Release, r.Release, ""),
		Arch:     pick(def.Arch, r.Arch, "auto"),
		Fidelity: Fidelity(pick(def.Fidelity, r.Fidelity, string(Basic))),
		Packages: mergeList(def.Packages, r.Packages),
		Fixtures: mergeList(def.Fixtures, r.Fixtures),
	}
	if out.ID == "" {
		return Router{}, errors.New("id is required")
	}
	out.Hostname = pick(def.Hostname, r.Hostname, defaultHostname(out.ID))

	if r.Ports != nil {
		out.Ports = *r.Ports
	} else if def.Ports != nil {
		out.Ports = *def.Ports
	}
	// Auto-assign anything left at zero. Deterministic in list order, so a
	// given config always produces the same URLs.
	if out.Ports.HTTP == 0 {
		out.Ports.HTTP = 8080 + index
	}
	if out.Ports.SSH == 0 {
		out.Ports.SSH = 2222 + index
	}

	if out.Release == "" {
		return Router{}, errors.New("release is required (e.g. \"25.12.4\" or \"snapshot\")")
	}
	t, err := LookupTarget(out.Arch)
	if err != nil {
		return Router{}, err
	}
	out.target = t
	// Record what "auto" resolved to, so every later message names a real
	// architecture instead of the word auto.
	out.Arch = t.Arch

	if len(out.Packages) == 0 {
		// luci-light is LuCI plus the handful of packages that make it
		// usable; a bare `luci` renders almost nothing.
		out.Packages = []string{"luci-light"}
	}

	// Extra packages accumulate: a default set plus whatever the router adds.
	// There is no subtraction syntax here because these are named by URL, and
	// a list short enough to write out is short enough to edit.
	out.Extra = append(append([]ExtraPackage(nil), def.Extra...), r.Extra...)
	for i, e := range out.Extra {
		if e.APK == "" && e.IPK == "" {
			return Router{}, fmt.Errorf("extra_packages[%d]: needs at least one of apk: or ipk:", i)
		}
		if e.Name == "" {
			url := e.APK
			if url == "" {
				url = e.IPK
			}
			out.Extra[i].Name = path.Base(url)
		}
	}

	if len(out.Fixtures) == 0 {
		out.Fixtures = DefaultFixtures()
	} else {
		if err := validateFixtures(out.ID, out.Fixtures); err != nil {
			return Router{}, err
		}
		out.Fixtures = expandFixtures(out.Fixtures)
	}
	return out, nil
}

// mergeList applies a router's package/fixture list on top of the defaults.
//
// An entry prefixed with + adds to the defaults and one prefixed with -
// removes from them. A list with no prefixes at all replaces the defaults
// outright, which is what someone writing an explicit list expects.
func mergeList(def, override []string) []string {
	if len(override) == 0 {
		return append([]string(nil), def...)
	}
	relative := false
	for _, s := range override {
		if strings.HasPrefix(s, "+") || strings.HasPrefix(s, "-") {
			relative = true
			break
		}
	}
	if !relative {
		return append([]string(nil), override...)
	}

	out := append([]string(nil), def...)
	for _, s := range override {
		switch {
		case strings.HasPrefix(s, "+"):
			name := strings.TrimPrefix(s, "+")
			if !contains(out, name) {
				out = append(out, name)
			}
		case strings.HasPrefix(s, "-"):
			name := strings.TrimPrefix(s, "-")
			out = remove(out, name)
		default:
			if !contains(out, s) {
				out = append(out, s)
			}
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func remove(list []string, s string) []string {
	out := list[:0]
	for _, v := range list {
		if v != s {
			out = append(out, v)
		}
	}
	return out
}

var idRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*$`)

func (c *Config) validate() error {
	seenID := map[string]bool{}
	seenHTTP := map[int]string{}
	seenSSH := map[int]string{}

	for i := range c.Routers {
		r := &c.Routers[i]
		if !idRE.MatchString(r.ID) {
			return fmt.Errorf("router %q: id must match [a-z0-9][a-z0-9_.-]* (it becomes a container name)", r.ID)
		}
		if seenID[r.ID] {
			return fmt.Errorf("duplicate router id %q", r.ID)
		}
		seenID[r.ID] = true

		if _, ok := LookupDistro(r.Distro); !ok {
			return fmt.Errorf("router %q: unknown distro %q (known: %s)",
				r.ID, r.Distro, strings.Join(KnownDistros(), ", "))
		}
		switch r.Fidelity {
		case Basic, Full, VM:
		default:
			return fmt.Errorf("router %q: unknown fidelity %q (known: basic, full, vm)", r.ID, r.Fidelity)
		}
		if r.Fidelity == VM && r.target.QEMUSystem == "" {
			return fmt.Errorf("router %q: fidelity vm is not supported for arch %s", r.ID, r.Arch)
		}
		if other, dup := seenHTTP[r.Ports.HTTP]; dup {
			return fmt.Errorf("routers %q and %q both use host http port %d", other, r.ID, r.Ports.HTTP)
		}
		if other, dup := seenSSH[r.Ports.SSH]; dup {
			return fmt.Errorf("routers %q and %q both use host ssh port %d", other, r.ID, r.Ports.SSH)
		}
		seenHTTP[r.Ports.HTTP] = r.ID
		seenSSH[r.Ports.SSH] = r.ID
	}
	return nil
}

// Router looks up a router by id.
func (c *Config) Router(id string) (*Router, bool) {
	for i := range c.Routers {
		if c.Routers[i].ID == id {
			return &c.Routers[i], true
		}
	}
	return nil, false
}

// Select returns the named routers, or all of them when no ids are given.
func (c *Config) Select(ids []string) ([]*Router, error) {
	if len(ids) == 0 {
		out := make([]*Router, 0, len(c.Routers))
		for i := range c.Routers {
			out = append(out, &c.Routers[i])
		}
		return out, nil
	}
	out := make([]*Router, 0, len(ids))
	for _, id := range ids {
		r, ok := c.Router(id)
		if !ok {
			return nil, fmt.Errorf("no router %q in %s (have: %s)", id, FileName, strings.Join(c.RouterIDs(), ", "))
		}
		out = append(out, r)
	}
	return out, nil
}

// RouterIDs lists every router id in file order.
func (c *Config) RouterIDs() []string {
	out := make([]string, 0, len(c.Routers))
	for i := range c.Routers {
		out = append(out, c.Routers[i].ID)
	}
	return out
}

// defaultHostname turns a router id into something that looks like a router
// hostname in LuCI's header, since the id is often terse.
func defaultHostname(id string) string {
	h := strings.Map(func(r rune) rune {
		if r == '_' || r == '.' {
			return '-'
		}
		return r
	}, id)
	return h
}
