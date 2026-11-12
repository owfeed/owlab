package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"owfeed.org/owlab/internal/config"
)

// A router held by one session makes the next wait, unless it passed --no-lock.
func TestLockRoutersWaitsUnlessNoLock(t *testing.T) {
	a := &app{cfg: &config.Config{Dir: t.TempDir()}}
	routers := []*config.Router{{ID: "owrt2512"}, {ID: "owrt2410"}}

	release, err := a.lockRouters(context.Background(), routers, false)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	// The default lock is the first thing a VM-only project writes into .owlab.
	if _, err := os.Stat(filepath.Join(a.cfg.Dir, ".owlab", ".gitignore")); err != nil {
		t.Fatalf("no .owlab/.gitignore beside the locks: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := a.lockRouters(ctx, routers[1:], false); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second session on a held router: err = %v, want a deadline", err)
	}
	skip, err := a.lockRouters(ctx, routers, true)
	if err != nil {
		t.Fatalf("--no-lock waited or failed: %v", err)
	}
	skip()
}
