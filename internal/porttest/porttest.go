package porttest

import (
	"errors"
	"net"
	"syscall"
	"testing"
	"time"
)

const belowEveryEphemeralPort = "127.0.0.1:1"

func RefusingAddress(t testing.TB) string {
	t.Helper()
	conn, err := net.DialTimeout("tcp", belowEveryEphemeralPort, time.Second)
	if err == nil {
		conn.Close()
		t.Fatalf("something listens at %s, where these tests need a port that refuses connections", belowEveryEphemeralPort)
	}
	if !errors.Is(err, syscall.ECONNREFUSED) {
		t.Fatalf("dialing %s = %v, want the connection refused", belowEveryEphemeralPort, err)
	}
	return belowEveryEphemeralPort
}
