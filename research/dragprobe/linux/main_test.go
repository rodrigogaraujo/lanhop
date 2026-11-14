//go:build linux

package main

import (
	"path/filepath"
	"testing"
	"time"
)

func TestDropTakesOneFileOnEitherSideOfItsFlags(t *testing.T) {
	c, err := parse([]string{"drop", "notes.txt", "-now", "-finish-wait", "3s"})

	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want, _ := filepath.Abs("notes.txt")
	if c.file != want || !c.now || c.finishWait != 3*time.Second || c.statusWait != 2*time.Second {
		t.Errorf("parsed %+v", c)
	}
}

func TestCancelWaits800MillisecondsAndReleasesOnlyByDefault(t *testing.T) {
	c, err := parse([]string{"cancel", "-display", ":7"})

	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.wait != 800*time.Millisecond || c.pressFirst || c.display != ":7" {
		t.Errorf("parsed %+v", c)
	}
}

func TestGrabMovesByHundredAndFiftyWithABaseline(t *testing.T) {
	c, err := parse([]string{"grab"})

	if err != nil || c.dx != 100 || c.dy != 50 || !c.baseline {
		t.Errorf("parsed %+v, %v", c, err)
	}
}

func TestWrongCommandLinesAreRefused(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"paste"},
		{"drop"},
		{"drop", "a", "b"},
		{"watch", "file"},
		{"grab", "-dx", "x"},
		{"cancel", "-gap", "1001ms"},
		{"cancel", "-gap", "-1ms"},
	} {
		if _, err := parse(args); err == nil {
			t.Errorf("parse(%q) was accepted", args)
		}
	}
}
