package config

import (
	"fmt"
	"strings"
)

// SynthOptions describes routers asked for on the command line rather than in
// a file.
//
// This exists for CI. A luci-app maintainer adding owlab to a workflow has no
// owlab.yaml and should not have to write one to answer "does my package
// install on 24.10 and 25.12" — the two release numbers ARE the configuration.
// Everything else is defaulted the way the file would default it, and the
// result goes through exactly the same merge and validation, so a synthesized
// router and a written one cannot drift apart.
type SynthOptions struct {
	// Name is the project name, and so the container name prefix.
	Name string
	// Dir is where .owlab/ is written. Normally the working directory.
	Dir string
	// Distro is openwrt or immortalwrt; empty means openwrt.
	Distro Distro
	// Releases is one router per entry.
	Releases []string
	// Arch is the target architecture; empty means the host's.
	Arch string
	// Packages replaces the default package set when non-empty, and accepts
	// the same +/- prefixes the file does.
	Packages []string
	// Fixtures replaces the default fixture profiles when non-empty.
	Fixtures []string
}

// Synthesize builds a config for routers named on the command line.
func Synthesize(o SynthOptions) (*Config, error) {
	if len(o.Releases) == 0 {
		return nil, fmt.Errorf("no releases given")
	}
	distro := o.Distro
	if distro == "" {
		distro = OpenWrt
	}
	if _, ok := LookupDistro(distro); !ok {
		return nil, fmt.Errorf("unknown distro %q (known: %s)", distro, strings.Join(KnownDistros(), ", "))
	}

	name := o.Name
	if name == "" {
		name = "owlab"
	}
	cfg := &Config{Version: 1, Dir: o.Dir, Project: Project{Name: name, Install: DefaultInstall()}}

	// The package list goes in as a router-level list, not as the defaults, so
	// that `--packages +luci-app-sqm` means the same thing on the command line
	// as it does in the file: added to the stock router set, rather than a
	// package literally named "+luci-app-sqm".
	def := rawRouter{
		Distro: strPtr(string(distro)),
		Arch:   strPtr(orDefault(o.Arch, "auto")),
	}
	// Shared across the synthesised routers: a port free for one is not free for the
	// next once it has been handed out.
	taken := map[int]bool{}
	for i, rel := range o.Releases {
		rel = strings.TrimSpace(rel)
		if rel == "" {
			continue
		}
		id := SynthID(distro, rel)
		r, err := merge(def, rawRouter{
			ID:       strPtr(id),
			Release:  strPtr(rel),
			Packages: o.Packages,
			Fixtures: o.Fixtures,
			Ports:    &Ports{HTTP: FreePortFrom(8080+i, taken), SSH: FreePortFrom(2222+i, taken)},
		}, i)
		if err != nil {
			return nil, fmt.Errorf("release %q: %w", rel, err)
		}
		cfg.Routers = append(cfg.Routers, r)
	}
	if len(cfg.Routers) == 0 {
		return nil, fmt.Errorf("no releases given")
	}
	// The same validation a file gets: duplicate releases, port collisions and
	// unsupported architectures are all reachable from flags too.
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// SynthID is the router id given to a release named on the command line.
//
// It carries the distro and the exact release because it becomes the container
// name, and a CI log listing three containers called router1..3 says nothing
// about which release failed.
func SynthID(d Distro, release string) string {
	id := strings.ToLower(string(d) + "-" + release)
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r == '-' || r == '.' || r == '_':
			return r
		}
		return '-'
	}, id)
}

func strPtr(s string) *string { return &s }

func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
