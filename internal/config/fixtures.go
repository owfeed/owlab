package config

import (
	"fmt"
	"sort"
	"strings"
)

// Fixture profiles invent the configuration a real router has, because LuCI
// renders almost nothing without it. A container with one interface and no
// clients shows a fraction of the widget surface a theme or an app has to
// style, so a whole class of bugs cannot be seen on a bare box.
//
// They are separate profiles rather than one blob because they have costs:
// `networks` adds interfaces to every network page, `wifi` claims
// /etc/config/wireless outright, and someone developing a specific app may
// want none of it.
const (
	// FixtureNetworks adds a WAN, a guest bridge, two VLANs, a disabled
	// interface, static routes, DHCP pools and the firewall zones for them.
	FixtureNetworks = "networks"
	// FixtureClients adds static leases, local hostnames, and at boot the
	// fake DHCP leases and ARP neighbours that put rows in the data tables.
	FixtureClients = "clients"
	// FixtureWireGuard adds a wg0 tunnel with two peers, plus its zone.
	FixtureWireGuard = "wireguard"
	// FixturePortForwards adds port forwards, one of them disabled.
	FixturePortForwards = "portforwards"
	// FixtureSystem adds LEDs, mounts, swap and cron entries.
	FixtureSystem = "system"
	// FixtureWiFi seeds /etc/config/wireless so the wireless pages render on
	// a box with no radios at all.
	FixtureWiFi = "wifi"

	// FixtureNone explicitly asks for a bare router.
	FixtureNone = "none"
)

var knownFixtures = []string{
	FixtureNetworks,
	FixtureClients,
	FixtureWireGuard,
	FixturePortForwards,
	FixtureSystem,
	FixtureWiFi,
}

// fixtureAliases name useful combinations.
var fixtureAliases = map[string][]string{
	// The default: a router that looks lived-in without inventing a VPN or
	// claiming the wireless config.
	"lived-in": {FixtureNetworks, FixtureClients, FixturePortForwards, FixtureSystem},
	"all":      knownFixtures,
}

// fixtureDeps records what a profile needs to make sense. They are added
// rather than reported as an error: asking for port forwards and getting them
// drawn against a missing zone would be a worse outcome than quietly getting
// the zone too.
var fixtureDeps = map[string][]string{
	FixturePortForwards: {FixtureNetworks},
	FixtureWireGuard:    {FixtureNetworks},
	FixtureClients:      {FixtureNetworks},
}

// DefaultFixtures is what a router gets when the config says nothing. A bare
// router is not a useful place to develop LuCI, so the default is the
// lived-in set rather than nothing.
func DefaultFixtures() []string { return expandFixtures([]string{"lived-in"}) }

// KnownFixtures lists the profiles and aliases, for error messages.
func KnownFixtures() []string {
	out := append([]string(nil), knownFixtures...)
	for k := range fixtureAliases {
		out = append(out, k)
	}
	out = append(out, FixtureNone)
	sort.Strings(out)
	return out
}

// expandFixtures resolves aliases and adds dependencies, preserving the
// canonical profile order so the result is stable.
func expandFixtures(in []string) []string {
	set := map[string]bool{}
	var add func(string)
	add = func(name string) {
		if alias, ok := fixtureAliases[name]; ok {
			for _, a := range alias {
				add(a)
			}
			return
		}
		if set[name] {
			return
		}
		set[name] = true
		for _, dep := range fixtureDeps[name] {
			add(dep)
		}
	}
	for _, name := range in {
		if name == FixtureNone {
			return nil
		}
		add(name)
	}

	out := make([]string, 0, len(set))
	for _, name := range knownFixtures {
		if set[name] {
			out = append(out, name)
		}
	}
	return out
}

// validateFixtures rejects names that are neither a profile nor an alias, so
// a typo is reported rather than silently producing a barer router than
// asked for.
func validateFixtures(routerID string, in []string) error {
	for _, name := range in {
		if name == FixtureNone {
			continue
		}
		if _, ok := fixtureAliases[name]; ok {
			continue
		}
		known := false
		for _, k := range knownFixtures {
			if k == name {
				known = true
				break
			}
		}
		if !known {
			return fmt.Errorf("router %q: unknown fixture %q (known: %s)",
				routerID, name, strings.Join(KnownFixtures(), ", "))
		}
	}
	return nil
}
