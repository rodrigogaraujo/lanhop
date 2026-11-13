//go:build unix

package main

import (
	"os"
	"path/filepath"
	"syscall"
)

const updateLockName = "update.lock"

func waitForTheUpdateLock(directory string) (func(), error) {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(directory, updateLockName), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		lock.Close()
		return nil, err
	}
	return func() {
		syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		lock.Close()
	}, nil
}
