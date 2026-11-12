package event

import "testing"

func TestControlEventsAreOutsideTheKernelTypeRange(t *testing.T) {
	const kernelTypeMax = 0x1f
	for name, e := range map[string]Event{"hello": Hello(), "ping": Ping(), "enter": Enter(), "leave": Leave()} {
		if !e.IsControl() {
			t.Errorf("%s is not a control event", name)
		}
		if e.Type <= kernelTypeMax {
			t.Errorf("%s has type %#x, inside the kernel range", name, e.Type)
		}
	}
}

func TestInputEventsAreNotControlEvents(t *testing.T) {
	for name, e := range map[string]Event{
		"key":      Key(KeyA, KeyPressed),
		"relative": Relative(RelX, 1),
		"sync":     Sync(),
	} {
		if e.IsControl() {
			t.Errorf("%s is reported as a control event", name)
		}
	}
}

func TestHelloCarriesTheProtocolVersion(t *testing.T) {
	if got := Hello().Value; got != ProtocolVersion {
		t.Errorf("Hello value = %d, want %d", got, ProtocolVersion)
	}
}

func TestControlCodesAreDistinct(t *testing.T) {
	seen := map[uint16]bool{}
	for _, e := range []Event{Hello(), Ping(), Enter(), Leave()} {
		if seen[e.Code] {
			t.Errorf("control code %d is used twice", e.Code)
		}
		seen[e.Code] = true
	}
}
