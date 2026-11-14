//go:build darwin || linux

package arrival

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

const noFollow = syscall.O_NOFOLLOW

func freeSpaceOf(directory string) (int64, error) {
	var disk syscall.Statfs_t
	if err := syscall.Statfs(directory, &disk); err != nil {
		return 0, err
	}
	free := uint64(disk.Bavail) * uint64(disk.Bsize)
	if free > plenty {
		return plenty, nil
	}
	return int64(free), nil
}

func flush(file *os.File) error {
	raw, err := file.SyscallConn()
	if err != nil {
		return err
	}
	var flushed error
	if err := raw.Control(func(descriptor uintptr) { flushed = syscall.Fsync(int(descriptor)) }); err != nil {
		return err
	}
	return flushed
}

func ownedByThisUser(info fs.FileInfo) bool {
	system, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(system.Uid) == os.Getuid()
}

func lockFolder(path string) (*os.File, error) {
	lock, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|noFollow, 0o600)
	if err != nil {
		return nil, fmt.Errorf("arrival: %w", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("%w: %s", ErrFolderInUse, path)
		}
		return nil, fmt.Errorf("arrival: %w", err)
	}
	return lock, nil
}
