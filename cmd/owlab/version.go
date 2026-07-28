package main

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
)

// Stamped by the release build with -ldflags -X. A build from source leaves
// them empty and the values below come from the Go module system instead, so
// `owlab version` says something true either way.
var (
	version = ""
	commit  = ""
	date    = ""
)

// Version is what this binary calls itself.
//
// The release build passes a tag. `go install ...@latest` passes nothing, but
// the module system records the version it resolved, so that case answers
// correctly too. Only a local `go build` has neither, and "dev" is then the
// honest answer.
func Version() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		v := strings.TrimPrefix(info.Main.Version, "v")
		// A pseudo-version is what the toolchain synthesizes when there is no
		// tag to name — `0.0.0-20260728071814-cd03c9db9630`, and `+dirty` when
		// the tree has uncommitted changes. Printing that as the version would
		// be worse than saying nothing: it looks like a release number and is
		// not one. The commit is reported separately anyway.
		if v != "" && v != "(devel)" && !strings.HasPrefix(v, "0.0.0-") && !strings.HasSuffix(v, "+dirty") {
			return v
		}
	}
	return "dev"
}

// versionLine is what `owlab version` prints.
//
// The commit and the build date matter more here than in most tools: owlab
// carries the image build context inside the binary, so "which owlab built
// this router" is a real question with a real answer, and a version number
// alone does not settle it for anything built between tags.
func versionLine() string {
	var b strings.Builder
	fmt.Fprintf(&b, "owlab %s", Version())

	details := []string{}
	if c := buildCommit(); c != "" {
		details = append(details, c)
	}
	if date != "" {
		details = append(details, date)
	}
	if len(details) > 0 {
		fmt.Fprintf(&b, " (%s)", strings.Join(details, ", "))
	}
	fmt.Fprintf(&b, "\n%s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	return b.String()
}

// buildCommit prefers the stamped value and falls back to what the Go
// toolchain embedded, which is present for any build from a git checkout.
func buildCommit() string {
	if commit != "" {
		return short(commit)
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	var rev, dirty string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "-dirty"
			}
		}
	}
	if rev == "" {
		return ""
	}
	return short(rev) + dirty
}

func short(rev string) string {
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}
