package porttest

import (
	"errors"
	"net"
	"strconv"
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

func TestARefusingAddressIsBelowEveryPortAListenerThatAsksForAnyPortIsGiven(t *testing.T) {
	refusing := portOf(t, RefusingAddress(t))
	for range 50 {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("Listen: %v", err)
		}
		given := portOf(t, listener.Addr().String())
		listener.Close()
		if given <= refusing {
			t.Fatalf("a listener that asked for any port was given %d, not above the refusing port %d", given, refusing)
		}
	}
}

func portOf(t *testing.T, address string) int {
	t.Helper()
	_, text, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(text)
	if err != nil {
		t.Fatal(err)
	}
	return port
}
