package main

import (
	"context"
	"fmt"
	"os"

	"owfeed.org/owlab/internal/config"
	"owfeed.org/owlab/internal/lock"
)

// lockRouters takes the lock of every router a writing command (up, down,
// sync, test) is about to touch, and returns what releases them.
//
// Without it a second session — another terminal, an agent, a project's own
// gate — rebuilt or synced a router while a gate was measuring it, and the gate
// reported on a build it never made (issue #39). Read-only commands take none:
// waiting to read a log behind a 20-minute test helps nobody.
//
// skip is --no-lock (or OWLAB_NO_LOCK=1), for a lock left by something that is still running and
// is known not to matter.
func (a *app) lockRouters(ctx context.Context, routers []*config.Router, skip bool) (func(), error) {
	// The variable is for a gate that holds the shared lock and runs owlab
	// under it: one flag missed there waits for the gate forever.
	if skip || os.Getenv("OWLAB_NO_LOCK") == "1" {
		return func() {}, nil
	}
	if a.cfg.Project.Lock == "" {
		if _, err := config.WorkDir(a.cfg.Dir); err != nil {
			return nil, err
		}
	}
	paths := make([]string, len(routers))
	for i, r := range routers {
		paths[i] = a.cfg.LockPath(r.ID)
	}
	return lock.Acquire(ctx, paths, func(path string) {
		fmt.Fprintf(os.Stderr, "owlab: another session holds %s; waiting for it\n", path)
	})
}
