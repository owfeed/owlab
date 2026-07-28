package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/VizzleTF/owlab/internal/config"
)

// extractConfigFlag pulls --config/-c out of the argument list wherever it
// appears, and returns the rest untouched.
//
// A global flag rather than one declared per command, because it applies to
// every command and Go's flag package cannot parse one before the subcommand
// name. Removing it here means each command's own FlagSet never sees it and
// nothing has to be taught about it.
//
// -c and not -f: `owlab logs -f` already means follow, and a flag that means
// two things depending on the command is worse than a longer name.
func extractConfigFlag(args []string) (rest []string, path string, err error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--config" || a == "-c":
			if i+1 >= len(args) {
				return nil, "", fmt.Errorf("%s needs a path", a)
			}
			path = args[i+1]
			i++
		case strings.HasPrefix(a, "--config="):
			path = strings.TrimPrefix(a, "--config=")
		case strings.HasPrefix(a, "-c="):
			path = strings.TrimPrefix(a, "-c=")
		default:
			rest = append(rest, a)
			continue
		}
		if path == "" {
			return nil, "", fmt.Errorf("%s needs a path", a)
		}
	}
	return rest, path, nil
}

// resolveConfig decides which owlab.yaml to read.
//
// Precedence: the flag, then OWLAB_CONFIG, then the search upward from the
// working directory. The env var exists for the case the flag does not cover
// well — a shell where every owlab command in a session should point at the
// same project, without repeating the path.
func resolveConfig(flagPath string) (string, error) {
	if flagPath != "" {
		return resolveExplicit(flagPath, "--config")
	}
	if env := os.Getenv("OWLAB_CONFIG"); env != "" {
		return resolveExplicit(env, "OWLAB_CONFIG")
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	path, err := config.Find(cwd)
	if err != nil {
		return "", fmt.Errorf("%w\n\n"+
			"Create one with the routers you want, or point owlab at an existing one:\n"+
			"  owlab --config path/to/owlab.yaml up\n"+
			"See `owlab doctor` for what this machine supports", err)
	}
	return path, nil
}

// resolveExplicit accepts either the file or the directory holding it, because
// `--config ../other-project` is what people type and failing on it would be a
// distinction without a reason.
func resolveExplicit(p, source string) (string, error) {
	st, err := os.Stat(p)
	if err != nil {
		return "", fmt.Errorf("%s: %w", source, err)
	}
	if st.IsDir() {
		p = filepath.Join(p, config.FileName)
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("%s: %w", source, err)
		}
	}
	return filepath.Abs(p)
}
