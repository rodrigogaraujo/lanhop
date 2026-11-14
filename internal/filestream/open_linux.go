package filestream

import (
	"os"
	"path/filepath"
	"syscall"
)

func openWithoutFollowing(path string) (*os.File, error) {
	return os.OpenFile(path, readingWithoutFollowing, 0)
}

func openInside(folder *os.File, folderPath, name string) (*os.File, error) {
	raw, err := folder.SyscallConn()
	if err != nil {
		return nil, err
	}
	var fd int
	var openErr error
	err = raw.Control(func(folderFd uintptr) {
		for {
			fd, openErr = syscall.Openat(int(folderFd), name, readingWithoutFollowing|syscall.O_CLOEXEC, 0)
			if openErr != syscall.EINTR {
				return
			}
		}
	})
	path := filepath.Join(folderPath, name)
	if err != nil {
		return nil, &os.PathError{Op: "openat", Path: path, Err: err}
	}
	if openErr != nil {
		return nil, &os.PathError{Op: "openat", Path: path, Err: openErr}
	}
	return os.NewFile(uintptr(fd), path), nil
}
