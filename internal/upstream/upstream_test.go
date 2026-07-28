package upstream

import (
	"reflect"
	"testing"
)

func TestSortIsNumericNotLexical(t *testing.T) {
	// The case this exists for: as strings, "24.10.10" sorts before
	// "24.10.9". A branch reaching double digits would then silently pick the
	// older release as "newest" — a downgrade nobody would look for.
	got := []string{"24.10.9", "24.10.10", "24.10.2", "25.12.1"}
	Sort(got)
	want := []string{"25.12.1", "24.10.10", "24.10.9", "24.10.2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Sort() = %v, want %v", got, want)
	}
}

func TestBranch(t *testing.T) {
	for in, want := range map[string]string{
		"25.12.4": "25.12",
		"24.10.8": "24.10",
		"24.10":   "24.10",
	} {
		if got := Branch(in); got != want {
			t.Errorf("Branch(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNewestStaysWithinItsBranch(t *testing.T) {
	all := []string{"25.12.5", "25.12.4", "25.12.3", "24.10.8", "24.10.7"}

	got := Newest(all, "25.12", 2)
	if want := []string{"25.12.5", "25.12.4"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Newest(25.12, 2) = %v, want %v", got, want)
	}

	// Asking for more than a branch has yields what it has, not a spill into
	// the next branch down — a 24.10 image built from a 25.12 rootfs would be
	// a broken router with a plausible-looking tag.
	got = Newest(all, "24.10", 5)
	if want := []string{"24.10.8", "24.10.7"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Newest(24.10, 5) = %v, want %v", got, want)
	}

	if got := Newest(all, "23.05", 2); len(got) != 0 {
		t.Errorf("Newest of an unknown branch = %v, want none", got)
	}
}

func TestPointReleaseMatchesTheListingAndSkipsPrereleases(t *testing.T) {
	// A trimmed copy of the shape downloads.openwrt.org serves.
	listing := `
		<a href="/">..</a>
		<a href="24.10.0-rc1/">24.10.0-rc1/</a>
		<a href="24.10.8/">24.10.8/</a>
		<a href="25.12.5/">25.12.5/</a>
		<a href="faillogs/">faillogs/</a>
	`
	var got []string
	for _, m := range pointRelease.FindAllStringSubmatch(listing, -1) {
		got = append(got, m[1])
	}
	want := []string{"24.10.8", "25.12.5"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("matched %v, want %v", got, want)
	}
}
