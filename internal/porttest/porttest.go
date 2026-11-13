//go:build unix

package porttest

import (
	"net"
	"strconv"
	"syscall"
	"testing"
)

var loopback = [4]byte{127, 0, 0, 1}

func RefusingAddress(t testing.TB) string {
	t.Helper()
	held, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("socket: %v", err)
	}
	syscall.CloseOnExec(held)
	t.Cleanup(func() { syscall.Close(held) })
	if err := syscall.Bind(held, &syscall.SockaddrInet4{Addr: loopback}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	bound, err := syscall.Getsockname(held)
	if err != nil {
		t.Fatalf("getsockname: %v", err)
	}
	return net.JoinHostPort(net.IP(loopback[:]).String(), strconv.Itoa(bound.(*syscall.SockaddrInet4).Port))
}
