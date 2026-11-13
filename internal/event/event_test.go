package event

import "testing"

func TestControlEventsAreOutsideTheKernelTypeRange(t *testing.T) {
	const kernelTypeMax = 0x1f
	for name, e := range map[string]Event{
		"hello":          Hello(),
		"ping":           Ping(),
		"enter":          Enter(),
		"leave":          Leave(),
		"features":       Features(FeatureClipboard),
		"clipboard":      Clipboard(1),
		"clipboard data": ClipboardData(0),
		"clipboard ack":  ClipboardAck(1),
		"unchanged":      ClipboardUnchanged(),
	} {
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
		"absolute": Absolute(AbsX, 1),
		"sync":     Sync(),
	} {
		if e.IsControl() {
			t.Errorf("%s is reported as a control event", name)
		}
	}
}

func TestAbsolutePositionsUseTheKernelEventTypeAndAxesFromZeroTo65535(t *testing.T) {
	const (
		kernelAbsoluteType uint16 = 0x03
		kernelAxisX        uint16 = 0x00
		kernelAxisY        uint16 = 0x01
	)
	tests := []struct {
		name string
		got  Event
		want Event
	}{
		{"the left edge", Absolute(AbsX, 0), Event{Type: kernelAbsoluteType, Code: kernelAxisX, Value: 0}},
		{"the right edge", Absolute(AbsX, AbsoluteMax), Event{Type: kernelAbsoluteType, Code: kernelAxisX, Value: 65535}},
		{"the top edge", Absolute(AbsY, 0), Event{Type: kernelAbsoluteType, Code: kernelAxisY, Value: 0}},
		{"the bottom edge", Absolute(AbsY, AbsoluteMax), Event{Type: kernelAbsoluteType, Code: kernelAxisY, Value: 65535}},
	}
	for _, test := range tests {
		if test.got != test.want {
			t.Errorf("%s = %+v, want %+v", test.name, test.got, test.want)
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
	for _, e := range []Event{Hello(), Ping(), Enter(), Leave(), Features(0), Clipboard(1), ClipboardData(0), ClipboardAck(1), ClipboardUnchanged()} {
		if seen[e.Code] {
			t.Errorf("control code %d is used twice", e.Code)
		}
		seen[e.Code] = true
	}
}

func TestClipboardFramesKeepTheirControlCodes(t *testing.T) {
	const control uint16 = 0xFF00
	tests := []struct {
		name string
		got  Event
		want Event
	}{
		{"features", Features(FeatureClipboard), Event{Type: control, Code: 5, Value: 1}},
		{"clipboard", Clipboard(9), Event{Type: control, Code: 6, Value: 9}},
		{"clipboard data", ClipboardData(-1), Event{Type: control, Code: 7, Value: -1}},
		{"clipboard ack", ClipboardAck(512), Event{Type: control, Code: 8, Value: 512}},
		{"clipboard unchanged", ClipboardUnchanged(), Event{Type: control, Code: 9}},
	}
	for _, test := range tests {
		if test.got != test.want {
			t.Errorf("%s = %+v, want %+v", test.name, test.got, test.want)
		}
	}
}
