//go:build linux && (386 || amd64 || arm || arm64 || loong64 || riscv64)

package evdev

import (
	"os"
	"syscall"
	"unsafe"
)

const (
	directionWrite uintptr = 1
	directionRead  uintptr = 2
)

func request(direction, kind, number, size uintptr) uintptr {
	return direction<<30 | size<<16 | kind<<8 | number
}

func ioctlValue(file *os.File, req, value uintptr) error {
	return withDescriptor(file, func(fd uintptr) syscall.Errno {
		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, req, value)
		return errno
	})
}

func ioctlPointer(file *os.File, req uintptr, pointer unsafe.Pointer) error {
	return withDescriptor(file, func(fd uintptr) syscall.Errno {
		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(pointer))
		return errno
	})
}

func withDescriptor(file *os.File, call func(fd uintptr) syscall.Errno) error {
	conn, err := file.SyscallConn()
	if err != nil {
		return err
	}
	var errno syscall.Errno
	if err := conn.Control(func(fd uintptr) {
		errno = call(fd)
		for errno == syscall.EINTR {
			errno = call(fd)
		}
	}); err != nil {
		return err
	}
	if errno != 0 {
		return errno
	}
	return nil
}

func textBeforeNUL(buffer []byte) string {
	for i, b := range buffer {
		if b == 0 {
			return string(buffer[:i])
		}
	}
	return string(buffer)
}
