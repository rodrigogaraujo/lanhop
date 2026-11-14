//go:build darwin && cgo

package main

import (
	"testing"
	"time"
)

func TestPromiseTakesAWholePathAndDragsThreeHundredPointsRight(t *testing.T) {
	c, err := parseCommand([]string{"promise", "~/Desktop/report.pdf"}, "/Users/someone")

	if err != nil {
		t.Fatalf("parseCommand: %v", err)
	}
	if c.file != "/Users/someone/Desktop/report.pdf" || c.dx != 300 || c.dy != 0 || c.to != nil || c.manual {
		t.Errorf("parsed %+v", c)
	}
	if c.timeout != 30*time.Second || c.logPath != "/Users/someone/Library/Logs/lanhop-dragprobe.log" {
		t.Errorf("timeout %v, log %q", c.timeout, c.logPath)
	}
}

func TestAManualPromiseWaitsLongerAndFlagsMayFollowTheFile(t *testing.T) {
	c, err := parseCommand([]string{"promise", "/tmp/a.txt", "-manual", "-to", "800, 400"}, "/Users/someone")

	if err != nil {
		t.Fatalf("parseCommand: %v", err)
	}
	if !c.manual || c.timeout != 90*time.Second || c.to == nil || *c.to != (point{800, 400}) {
		t.Errorf("parsed %+v", c)
	}
}

func TestCancelWaits800MillisecondsByDefault(t *testing.T) {
	c, err := parseCommand([]string{"cancel", "-log", "~/probe.log"}, "/Users/someone")

	if err != nil || c.wait != 800*time.Millisecond || c.every != 20*time.Millisecond || c.logPath != "/Users/someone/probe.log" {
		t.Errorf("parsed %+v, %v", c, err)
	}
}

func TestWrongCommandLinesAreRefused(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"drop"},
		{"promise"},
		{"promise", "relative.txt"},
		{"promise", "/a", "/b"},
		{"promise", "/a", "-to", "800"},
		{"promise", "/a", "-steps", "0"},
		{"watch", "/a"},
		{"watch", "-every", "0s"},
	} {
		if _, err := parseCommand(args, "/Users/someone"); err == nil {
			t.Errorf("parseCommand(%q) was accepted", args)
		}
	}
}

func TestTheDropPointStaysOnAScreen(t *testing.T) {
	builtIn := rect{0, 0, 1440, 900}
	left := rect{-1920, 0, 1920, 1080}
	tests := []struct {
		name    string
		start   point
		dx, dy  float64
		to      *point
		want    point
		clamped bool
	}{
		{"to the right", point{500, 400}, 300, 0, nil, point{800, 400}, false},
		{"onto the other screen", point{100, 400}, -300, 0, nil, point{-200, 400}, false},
		{"past the right edge", point{1300, 400}, 300, 0, nil, point{1439, 400}, true},
		{"below every screen", point{500, 850}, 0, 300, nil, point{500, 899}, true},
		{"a point given", point{500, 400}, 300, 0, &point{-1000, 500}, point{-1000, 500}, false},
	}
	for _, test := range tests {
		got, clamped := dropTarget(test.start, test.dx, test.dy, test.to, []rect{builtIn, left})
		if got != test.want || clamped != test.clamped {
			t.Errorf("%s: %v, clamped %v; want %v, clamped %v", test.name, got, clamped, test.want, test.clamped)
		}
	}
}

func TestDragOperationsAreNamed(t *testing.T) {
	for operation, want := range map[int]string{
		-1:                               "no drag session ended",
		0:                                "none: nothing was dropped",
		operationCopy:                    "copy",
		operationCopy | operationGeneric: "copy+generic",
		operationMove | operationDelete:  "move+delete",
		operationLink | operationPrivate: "link+private",
	} {
		if got := operationName(operation); got != want {
			t.Errorf("operationName(%d) = %q, want %q", operation, got, want)
		}
	}
}
