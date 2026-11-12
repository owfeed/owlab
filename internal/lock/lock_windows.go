//go:build windows

package lock

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

// LockFileEx and its flags, from kernel32 rather than golang.org/x/sys/windows,
// which owlab does not depend on and would not otherwise need.
var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
)

const (
	lockfileFailImmediately               = 0x1
	lockfileExclusiveLock                 = 0x2
	errorLockViolation      syscall.Errno = 33
)

func tryLock(f *os.File) (bool, error) {
	var ol syscall.Overlapped
	r, _, err := procLockFileEx.Call(f.Fd(), lockfileExclusiveLock|lockfileFailImmediately,
		0, 1, 0, uintptr(unsafe.Pointer(&ol)))
	if r != 0 {
		return true, nil
	}
	if errors.Is(err, errorLockViolation) {
		return false, nil
	}
	return false, err
}

// unlock before the close: Windows releases a closed handle's locks "when
// resources allow", and the next session should not wait on that.
func unlock(f *os.File) error {
	var ol syscall.Overlapped
	r, _, err := procUnlockFileEx.Call(f.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&ol)))
	if r == 0 {
		return err
	}
	return nil
}
