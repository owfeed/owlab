package config

import (
	"strings"
	"testing"
)

func TestFixtureAliasExpandsAndOrders(t *testing.T) {
	got := expandFixtures([]string{"lived-in"})
	want := "networks clients portforwards system"
	if strings.Join(got, " ") != want {
		t.Errorf("got %q, want %q", strings.Join(got, " "), want)
	}
}

func TestFixtureDependenciesAreAdded(t *testing.T) {
	// portforwards draws every redirect against src='wan'; without the
	// networks profile that zone does not exist and the rows render against
	// nothing. Pulling the dependency in beats reporting an error the user
	// can only fix one way.
	got := expandFixtures([]string{"portforwards"})
	if strings.Join(got, " ") != "networks portforwards" {
		t.Errorf("got %q", strings.Join(got, " "))
	}
}

func TestFixtureOrderIsCanonicalNotUserOrder(t *testing.T) {
	// The profiles have ordering constraints between them, so the order the
	// user happened to type must not survive.
	got := expandFixtures([]string{"system", "wifi", "networks"})
	if strings.Join(got, " ") != "networks system wifi" {
		t.Errorf("got %q", strings.Join(got, " "))
	}
}

func TestFixtureNoneWins(t *testing.T) {
	if got := expandFixtures([]string{"lived-in", "none"}); len(got) != 0 {
		t.Errorf("none should produce a bare router, got %v", got)
	}
}

func TestUnknownFixtureIsRejected(t *testing.T) {
	err := validateFixtures("r", []string{"netwroks"})
	if err == nil {
		t.Fatal("a typo in a fixture name must be reported")
	}
	if !strings.Contains(err.Error(), "netwroks") {
		t.Errorf("error should quote the typo: %v", err)
	}
}

func TestDefaultFixturesAreLivedIn(t *testing.T) {
	p := write(t, `
version: 1
routers:
  - id: a
    release: "25.12.4"
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := cfg.Router("a")
	// A bare router is not a useful place to develop LuCI: most pages render
	// empty, so most styling goes unexercised.
	if len(a.Fixtures) == 0 {
		t.Fatal("expected default fixtures")
	}
	if contains(a.Fixtures, FixtureWiFi) {
		t.Error("wifi claims /etc/config/wireless outright and must be opt-in")
	}
}
