// Package lock keeps two sessions from writing the same router at once.
//
// The locks are advisory and exclusive, and on Linux and macOS they are
// flock(2) locks, the same kind `flock -x <file>` takes. That is the point: a
// project whose own gates already serialise on a lock file can name that file
// in owlab.yaml, and its gates and owlab then wait for each other instead of
// one measuring a router the other is rebuilding.
//
// A lock file is never deleted. Removing one while a waiter has it open hands
// the next session a fresh file and a lock nobody else is holding.
package lock

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// Poll is how often a held lock is tried again. Polled rather than a blocking
// flock, because Go restarts a blocking flock after a signal and Ctrl-C would
// then wait for the other session too.
var Poll = 250 * time.Millisecond

// Acquire takes every lock in paths and returns what releases them. It waits
// for a held lock until ctx ends, calling waiting once for each lock it has to
// wait for.
//
// Taken in Order, so two sessions that each need several of the same locks
// cannot each hold one the other is waiting for.
func Acquire(ctx context.Context, paths []string, waiting func(path string)) (release func(), err error) {
	var held []*os.File
	release = func() {
		for _, f := range slices.Backward(held) {
			_ = unlock(f)
			_ = f.Close()
		}
	}
	for _, p := range Order(paths) {
		f, err := take(ctx, p, waiting)
		if err != nil {
			release()
			return nil, err
		}
		held = append(held, f)
	}
	return release, nil
}

// Order is paths sorted, with duplicates removed. A duplicate would be a second
// open of the same file, and flock treats that as another holder: the session
// would wait for itself.
func Order(paths []string) []string {
	out := slices.Clone(paths)
	slices.Sort(out)
	return slices.Compact(out)
}

func take(ctx context.Context, path string, waiting func(string)) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	for announced := false; ; announced = true {
		ok, err := tryLock(f)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("lock %s: %w", path, err)
		}
		if ok {
			return f, nil
		}
		if !announced {
			waiting(path)
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, fmt.Errorf("waiting for %s: %w", path, ctx.Err())
		case <-time.After(Poll):
		}
	}
}
