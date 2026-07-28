package main

import (
	"flag"
	"strings"
	"testing"
)

// The dispatcher and the help text used to be two lists, and they had already
// drifted: `context` was in one and not the other. They are one table now, and
// this is what keeps it that way.
func TestEveryDispatchableCommandIsDocumentedOrDeliberatelyNot(t *testing.T) {
	// The only command that is deliberately undocumented. It prepares a build
	// context and prints arguments for `docker buildx build` — something the
	// publish workflow needs and a developer does not.
	hidden := map[string]bool{"context": true}

	usage := usage()
	for _, c := range commands {
		documented := c.summary != ""
		if hidden[c.name] {
			if documented {
				t.Errorf("%q is meant to stay out of the help text", c.name)
			}
			continue
		}
		if !documented {
			t.Errorf("%q is dispatchable but has no summary, so it is invisible in the help text", c.name)
		}
		if !strings.Contains(usage, "  "+c.name+" ") {
			t.Errorf("%q is missing from the rendered help text", c.name)
		}
	}
}

func TestCommandTableIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range commands {
		if c.name == "" {
			t.Error("a command with no name can never be typed")
		}
		if seen[c.name] {
			t.Errorf("duplicate command %q: the first one wins and the second is unreachable", c.name)
		}
		seen[c.name] = true
		if c.run == nil {
			t.Errorf("%q dispatches to nothing", c.name)
		}
	}

	// The commands that must work when everything else is broken. doctor
	// reports on config resolution itself, and version is a property of the
	// binary rather than of any project — neither may require a loadable
	// owlab.yaml or a running daemon.
	for _, name := range []string{"doctor", "version", "releases"} {
		c, ok := lookupCommand(name)
		if !ok {
			t.Fatalf("%q is not in the table", name)
		}
		if !c.bare {
			t.Errorf("%q must run without loading a config", name)
		}
	}
	// Everything else does need one; a command that skips the load would get a
	// nil config and panic rather than explain itself.
	//
	// `releases` is bare and still uses a config when there is one: it loads it
	// itself and falls back to the full listing when there is none. That is the
	// only shape allowed here, and it is why the exception is a list rather than
	// a flag — adding a name to it should require reading this.
	bare := map[string]bool{"doctor": true, "version": true, "releases": true}
	for _, c := range commands {
		if c.bare && !bare[c.name] {
			t.Errorf("%q skips the config load — is that deliberate?", c.name)
		}
	}
}

func TestLookupCommandRejectsUnknownNames(t *testing.T) {
	if _, ok := lookupCommand("nope"); ok {
		t.Error("an unknown command was resolved")
	}
	if _, ok := lookupCommand(""); ok {
		t.Error("the empty string resolved to a command")
	}
}

// `owlab up owrt2512 --rebuild` is what people type. Treating --rebuild as a
// router name would be worse than either alternative.
func TestParseMixedTakesFlagsBeforeOrAfterIDs(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantIDs []string
		wantFlg bool
	}{
		{"flag last", []string{"owrt2512", "--rebuild"}, []string{"owrt2512"}, true},
		{"flag first", []string{"--rebuild", "owrt2512"}, []string{"owrt2512"}, true},
		{"flag between", []string{"a", "--rebuild", "b"}, []string{"a", "b"}, true},
		{"no flag", []string{"a", "b"}, []string{"a", "b"}, false},
		{"flag only", []string{"--rebuild"}, nil, true},
		{"nothing", nil, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := flag.NewFlagSet("up", flag.ContinueOnError)
			fs.SetOutput(nopWriter{})
			rebuild := fs.Bool("rebuild", false, "")

			ids, err := parseMixed(fs, c.args)
			if err != nil {
				t.Fatal(err)
			}
			if *rebuild != c.wantFlg {
				t.Errorf("rebuild = %v, want %v", *rebuild, c.wantFlg)
			}
			if len(ids) != len(c.wantIDs) {
				t.Fatalf("ids = %v, want %v", ids, c.wantIDs)
			}
			for i := range ids {
				if ids[i] != c.wantIDs[i] {
					t.Errorf("ids = %v, want %v", ids, c.wantIDs)
				}
			}
		})
	}
}

func TestParseMixedReportsAnUnknownFlag(t *testing.T) {
	fs := flag.NewFlagSet("up", flag.ContinueOnError)
	fs.SetOutput(nopWriter{})
	fs.Bool("rebuild", false, "")
	if _, err := parseMixed(fs, []string{"owrt", "--nonsense"}); err == nil {
		t.Fatal("an unknown flag should be reported, not taken as a router id")
	}
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }
