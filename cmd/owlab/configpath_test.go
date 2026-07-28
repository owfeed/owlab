package main

import (
	"strings"
	"testing"
)

func TestExtractConfigFlagFindsItAnywhere(t *testing.T) {
	for _, tc := range []struct {
		in   []string
		rest []string
		path string
	}{
		{[]string{"--config", "a.yaml", "up"}, []string{"up"}, "a.yaml"},
		{[]string{"up", "-c", "a.yaml", "owrt2512"}, []string{"up", "owrt2512"}, "a.yaml"},
		{[]string{"--config=a.yaml", "up"}, []string{"up"}, "a.yaml"},
		{[]string{"-c=a.yaml", "up"}, []string{"up"}, "a.yaml"},
		{[]string{"up", "--rebuild"}, []string{"up", "--rebuild"}, ""},
		// -f belongs to `logs`. Taking it here would break following a log,
		// which is why the short form is -c.
		{[]string{"logs", "-f"}, []string{"logs", "-f"}, ""},
	} {
		rest, path, err := extractConfigFlag(tc.in)
		if err != nil {
			t.Errorf("%v: %v", tc.in, err)
			continue
		}
		if path != tc.path {
			t.Errorf("%v: path = %q, want %q", tc.in, path, tc.path)
		}
		if strings.Join(rest, " ") != strings.Join(tc.rest, " ") {
			t.Errorf("%v: rest = %v, want %v", tc.in, rest, tc.rest)
		}
	}

	if _, _, err := extractConfigFlag([]string{"--config"}); err == nil {
		t.Error("a --config with no path should be an error")
	}
}
