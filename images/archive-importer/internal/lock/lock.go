// Package lock provides an advisory file-lock guard for serialising
// archive-importer runs against the same archive id.
package lock

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

type Lock struct{ f *os.File }

// Acquire opens path and takes an exclusive non-blocking flock on it.
// Returns an error if another process holds the lock.
func Acquire(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("flock %q: %w", path, err)
	}
	return &Lock{f: f}, nil
}

// Release drops the lock. The lockfile remains on disk; the next caller
// re-acquires it without issue.
func (l *Lock) Release() error {
	if l.f == nil {
		return nil
	}
	unix.Flock(int(l.f.Fd()), unix.LOCK_UN)
	err := l.f.Close()
	l.f = nil
	return err
}
