package upgrade

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// InstallLock is the installation-level advisory lock coordinating the
// manager and the one-shot worker. It only synchronizes processes that follow
// the lock protocol (see flock research); it never implies the previous
// install finished safely, and it cannot stop same-UID terminal activity.
type InstallLock struct{ file *os.File }

// TryLock opens and locks <root>/task.lock without blocking. EWOULDBLOCK
// maps to (nil, false, nil): the caller reports a conflict, never queues.
func TryLock(root string) (*InstallLock, bool, error) {
	path := filepath.Join(root, "task.lock")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, false, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("lock %s: %w", path, err)
	}
	return &InstallLock{file: file}, true, nil
}

// Release drops the lock. Releasing an unlocked or closed lock is a no-op so
// deferred cleanup never panics on error paths.
func (l *InstallLock) Release() {
	if l == nil || l.file == nil {
		return
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	_ = l.file.Close()
	l.file = nil
}
