package main

import (
	"errors"
	"strings"
	"testing"

	"owfeed.org/owlab/internal/config"
)

// --no-build starts an image only when its label says it was built from what
// owlab.yaml describes now; each refusal says what to run instead.
func TestBuiltFromStartsOnlyAnImageOfTheseInputs(t *testing.T) {
	r := &config.Router{ID: "owrt2512"}
	const image, want = "owlab-demo-owrt2512:latest", "sha256:aaaa"
	cases := []struct {
		name, label string
		inspect     error
		refuse      string
	}{
		{name: "the inputs match", label: want},
		{name: "no image", inspect: errors.New("exit status 1"), refuse: "load one with `docker load`"},
		{name: "no label", label: "", refuse: "carries no org.owlab.inputs label"},
		{name: "other inputs", label: "sha256:bbbb", refuse: "built from other inputs"},
	}
	for _, c := range cases {
		err := builtFrom(r, image, c.label, c.inspect, want)
		if c.refuse == "" {
			if err != nil {
				t.Errorf("%s: refused: %v", c.name, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: started", c.name)
			continue
		}
		for _, s := range []string{c.refuse, "owrt2512", "owlab up owrt2512"} {
			if !strings.Contains(err.Error(), s) {
				t.Errorf("%s: %q does not say %q", c.name, err, s)
			}
		}
	}
}
