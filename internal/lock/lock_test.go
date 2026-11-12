package lock

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func init() { Poll = 10 * time.Millisecond }

// tryFor is Acquire with a deadline, reporting how many locks it waited for.
func tryFor(t *testing.T, d time.Duration, paths ...string) (func(), int, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	waits := 0
	release, err := Acquire(ctx, paths, func(string) { waits++ })
	return release, waits, err
}

func TestSecondAcquirerWaitsUntilRelease(t *testing.T) {
	p := filepath.Join(t.TempDir(), "locks", "r1.lock")
	release, _, err := tryFor(t, time.Second, p)
	if err != nil {
		t.Fatal(err)
	}

	_, waits, err := tryFor(t, 100*time.Millisecond, p)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second Acquire while held: err = %v, want a deadline", err)
	}
	if waits != 1 {
		t.Fatalf("waiting called %d times, want once", waits)
	}

	release()
	again, waits, err := tryFor(t, time.Second, p)
	if err != nil || waits != 0 {
		t.Fatalf("Acquire after release: err = %v, waits = %d", err, waits)
	}
	again()
}

func TestWaiterGetsTheLockWhenItIsReleased(t *testing.T) {
	p := filepath.Join(t.TempDir(), "r1.lock")
	release, _, err := tryFor(t, time.Second, p)
	if err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(50*time.Millisecond, release)
	got, waits, err := tryFor(t, 5*time.Second, p)
	if err != nil || waits != 1 {
		t.Fatalf("err = %v, waits = %d; want the lock after one wait", err, waits)
	}
	got()
}

// A failure part-way must not leave the locks already taken held.
func TestFailedAcquireReleasesWhatItTook(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.lock"), filepath.Join(dir, "b.lock")
	holdB, _, err := tryFor(t, time.Second, b)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := tryFor(t, 50*time.Millisecond, a, b); err == nil {
		t.Fatal("Acquire(a, b) succeeded while b was held")
	}
	holdB()
	release, waits, err := tryFor(t, time.Second, a)
	if err != nil || waits != 0 {
		t.Fatalf("a still held after a failed Acquire: err = %v, waits = %d", err, waits)
	}
	release()
}

// One lock file shared by every router (a lock: path with no {id}) is one
// lock, not two opens that wait for each other.
func TestSameFileTwiceIsOneLock(t *testing.T) {
	p := filepath.Join(t.TempDir(), "lab.lock")
	release, waits, err := tryFor(t, time.Second, p, p)
	if err != nil || waits != 0 {
		t.Fatalf("err = %v, waits = %d", err, waits)
	}
	release()
}

func TestOrderIsSortedAndUnique(t *testing.T) {
	got := Order([]string{"/l/c", "/l/a", "/l/c", "/l/b"})
	if want := []string{"/l/a", "/l/b", "/l/c"}; !slices.Equal(got, want) {
		t.Fatalf("Order = %v, want %v", got, want)
	}
}
