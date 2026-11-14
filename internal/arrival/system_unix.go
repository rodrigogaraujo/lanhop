//go:build darwin || linux

package arrival

import (
	"os"
	"syscall"
)

const (
	noFollow         = syscall.O_NOFOLLOW
	largestFreeSpace = 1 << 62
)

func freeSpaceOf(directory string) (int64, error) {
	var disk syscall.Statfs_t
	if err := syscall.Statfs(directory, &disk); err != nil {
		return 0, err
	}
	free := uint64(disk.Bavail) * uint64(disk.Bsize)
	if free > largestFreeSpace {
		return largestFreeSpace, nil
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
