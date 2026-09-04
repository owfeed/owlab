package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// The record a build leaves behind is the only thing standing between "one bad
// package no longer fails the build" and "one bad package is never mentioned
// again" (issue #12). Reading it wrong is silent in exactly the direction that
// matters: an empty list means "the router has everything it was told to have".
func TestMissingExtrasReadsOneNamePerLine(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want []string
	}{
		// The overwhelming majority: `cat` of a file that is not there, which
		// the script turns into empty output rather than a failure.
		{"nothing recorded", "", nil},
		{"one package", "01-example_1.0_all.ipk\n", []string{"01-example_1.0_all.ipk"}},
		{
			"several, with the blank line a trailing newline leaves",
			"example-daemon_1.0_all.ipk\nluci-app-example_1.0_all.ipk\n\n",
			[]string{"example-daemon_1.0_all.ipk", "luci-app-example_1.0_all.ipk"},
		},
		// docker exec hands back \r\n from a container with a tty attached.
		{"carriage returns", "example_1.0_all.ipk\r\n", []string{"example_1.0_all.ipk"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run := func(_ context.Context, _ string, _ []byte, out io.Writer) error {
				_, err := io.WriteString(out, tc.out)
				return err
			}
			got := missingExtras(t.Context(), run)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("missingExtras = %q, want %q", got, tc.want)
			}
		})
	}
}

// A router that cannot be reached is not a router that is missing a package.
// Reporting one as the other would put "is running WITHOUT" against a box that
// was simply stopped, and send the developer looking at the wrong thing.
func TestMissingExtrasReportsNothingWhenTheRouterCannotAnswer(t *testing.T) {
	run := func(_ context.Context, _ string, _ []byte, out io.Writer) error {
		_, _ = io.WriteString(out, "Error: No such container\n")
		return errors.New("exit status 1")
	}
	if got := missingExtras(t.Context(), run); got != nil {
		t.Errorf("missingExtras = %q, want none", got)
	}
}
