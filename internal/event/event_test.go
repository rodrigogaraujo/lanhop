package event

import (
	"math"
	"testing"
)

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
		"data hello":     DataHello(1),
		"file copy":      FileCopy(1),
		"file copy size": FileCopySize(1),
		"open data":      OpenData(1, true),
		"data stop":      DataStop(1),
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
	for _, e := range []Event{
		Hello(), Ping(), Enter(), Leave(), Features(0), Clipboard(1), ClipboardData(0), ClipboardAck(1), ClipboardUnchanged(),
		DataHello(1), FileCopy(1), FileCopySize(1), OpenData(1, false), DataStop(1),
	} {
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

func TestFileFramesKeepTheirControlCodes(t *testing.T) {
	const control uint16 = 0xFF00
	tests := []struct {
		name string
		got  Event
		want Event
	}{
		{"data hello", DataHello(7), Event{Type: control, Code: 10, Value: 7}},
		{"file copy", FileCopy(10000), Event{Type: control, Code: 11, Value: 10000}},
		{"file copy size of 4 GiB", FileCopySize(4 << 20), Event{Type: control, Code: 12, Value: 4194304}},
		{"open data, the central sends", OpenData(5, false), Event{Type: control, Code: 13, Value: 5}},
		{"open data, the side machine sends", OpenData(5, true), Event{Type: control, Code: 13, Value: math.MinInt32 | 5}},
		{"open data with the largest number", OpenData(math.MaxInt32, true), Event{Type: control, Code: 13, Value: -1}},
		{"data stop", DataStop(7), Event{Type: control, Code: 14, Value: 7}},
	}
	for _, test := range tests {
		if test.got != test.want {
			t.Errorf("%s = %+v, want %+v", test.name, test.got, test.want)
		}
	}
}

func TestFileFramesStayBetweenCodes10And14(t *testing.T) {
	for _, e := range []Event{DataHello(1), FileCopy(1), FileCopySize(1), OpenData(1, true), DataStop(1)} {
		if e.Code < 10 || e.Code > 14 {
			t.Errorf("a file frame uses control code %d, outside 10 to 14", e.Code)
		}
	}
}

func TestOpenDataGivesBackTheTransferNumberAndTheWay(t *testing.T) {
	for _, transfer := range []int32{1, 5, 1 << 30, TransferNumberMax} {
		for _, sideMachineSends := range []bool{false, true} {
			gotTransfer, gotWay := SplitOpenData(OpenData(transfer, sideMachineSends).Value)
			if gotTransfer != transfer || gotWay != sideMachineSends {
				t.Errorf("OpenData(%d, %t) splits into %d, %t", transfer, sideMachineSends, gotTransfer, gotWay)
			}
		}
	}
}

func TestTransferNumbersUseTheLow31Bits(t *testing.T) {
	if TransferNumberMax != 1<<31-1 {
		t.Errorf("TransferNumberMax = %d, want %d", TransferNumberMax, 1<<31-1)
	}
}
