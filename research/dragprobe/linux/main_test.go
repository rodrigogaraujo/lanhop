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

func TestTheEdgeIs40PixelsOnTheRightAndWaitsTwoMinutesForADrop(t *testing.T) {
	c, err := parse([]string{"edge"})

	if err != nil || c.edge != "right" || c.width != 40 || c.timeout != 2*time.Minute || c.after != 3*time.Second {
		t.Errorf("parsed %+v, %v", c, err)
	}
}

func TestReleaseRealWaitsTwoSecondsAndPutsUpTheEdgeOnlyWhenAsked(t *testing.T) {
	c, err := parse([]string{"release-real", "-edge", "left", "-width", "20"})

	if err != nil || c.wait != 2*time.Second || c.withEdge || c.edge != "left" || c.width != 20 || c.timeout != 2*time.Minute {
		t.Errorf("parsed %+v, %v", c, err)
	}
	c, err = parse([]string{"release-real", "-with-edge", "-wait", "4s"})
	if err != nil || !c.withEdge || c.wait != 4*time.Second || c.edge != "right" || c.width != 40 {
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
		{"edge", "file"},
		{"edge", "-edge", "middle"},
		{"edge", "-width", "0"},
		{"edge", "-width", "401"},
		{"edge", "-timeout", "0s"},
		{"edge", "-timeout", "11m"},
		{"edge", "-after", "31s"},
		{"release-real", "-wait", "0s"},
		{"release-real", "-wait", "11s"},
		{"release-real", "-after", "-1s"},
		{"release-real", "-with-edge", "-edge", "up"},
		{"release-real", "-press-first"},
	} {
		if _, err := parse(args); err == nil {
			t.Errorf("parse(%q) was accepted", args)
		}
	}
}
