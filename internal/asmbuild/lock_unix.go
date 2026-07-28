//go:build unix

package asmbuild

import (
	"os"
	"path/filepath"
	"syscall"
)

// lockDir takes an exclusive advisory lock on a lockfile inside dir,
// blocking until it is available, and returns the unlock function.
//
// flock is released automatically if the holding process dies, so a
// crashed or killed test binary cannot wedge every later build the way a
// hand-rolled O_EXCL lockfile would.
func lockDir(dir string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(dir, lockName), os.O_CREATE|os.O_RDWR, 0o666)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
