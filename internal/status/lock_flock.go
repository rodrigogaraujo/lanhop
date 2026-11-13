//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package status

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

var errTakenByAnother = errors.New("status: another lanhop on this machine keeps the status")

func takeLock(path string) (*os.File, error) {
	lock, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("status: %w", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errTakenByAnother
		}
		return nil, fmt.Errorf("status: %w", err)
	}
	return lock, nil
}

func lockIsTaken(path string) bool {
	lock, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return false
	}
	defer lock.Close()
	err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		return false
	}
	return errors.Is(err, syscall.EWOULDBLOCK)
}
