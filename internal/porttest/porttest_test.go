//go:build unix

package porttest

import (
	"errors"
	"net"
	"syscall"
	"testing"
	"time"
)

func TestARefusingAddressRefusesEveryConnection(t *testing.T) {
	address := RefusingAddress(t)

	for range 3 {
		conn, err := net.DialTimeout("tcp", address, time.Second)
		if err == nil {
			conn.Close()
			t.Fatalf("a connection to %s was taken", address)
		}
		if !errors.Is(err, syscall.ECONNREFUSED) {
			t.Fatalf("dialing %s = %v, want the connection refused", address, err)
		}
	}
}

func TestNoListenerCanTakeARefusingAddress(t *testing.T) {
	address := RefusingAddress(t)

	listener, err := net.Listen("tcp", address)
	if err == nil {
		listener.Close()
		t.Fatalf("a listener took %s while it was held", address)
	}
}
