//go:build linux

package x11

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

const badWindow = 3

type edgeOutcome struct {
	drop EdgeDrop
	err  error
}

type sayings struct {
	lock  sync.Mutex
	lines []string
}

func (s *sayings) say(line string) {
	s.lock.Lock()
	s.lines = append(s.lines, line)
	s.lock.Unlock()
}

func (s *sayings) all() []string {
	s.lock.Lock()
	defer s.lock.Unlock()
	return slices.Clone(s.lines)
}

func openEdgeFor(t *testing.T, display string, options EdgeOptions) (*Edge, *sayings) {
	t.Helper()
	heard := &sayings{}
	options.Say = heard.say
	edge, err := openDragsOn(t, display).OpenEdge(options)
	if err != nil {
		t.Fatalf("OpenEdge: %v", err)
	}
	t.Cleanup(func() {
		edge.Close()
		for _, line := range heard.all() {
			t.Log(line)
		}
	})
	return edge, heard
}

func serveOneDrop(edge *Edge, limit time.Duration) <-chan edgeOutcome {
	served := make(chan edgeOutcome, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), limit)
		defer cancel()
		drop, err := edge.NextDrop(ctx)
		served <- edgeOutcome{drop, err}
	}()
	return served
}

func windowAttributes(t *testing.T, c *conn, window uint32) (exists, viewable, overrideRedirect bool) {
	t.Helper()
	reply, err := c.call(newRequest(opGetWindowAttributes, 0).u32(window).done())
	var failed RequestError
	if errors.As(err, &failed) && failed.Code == badWindow {
		return false, false, false
	}
	if err != nil {
		t.Fatalf("GetWindowAttributes 0x%x: %v", window, err)
	}
	return true, reply[26] == mapStateViewable, reply[27] == 1
}

func TestTheEdgeTakesAWholeDropWithCopyAndIsGoneOnceClosed(t *testing.T) {
	display := startXvfb(t)
	edge, heard := openEdgeFor(t, display, EdgeOptions{Side: "right", Width: 40})
	observer := dialForTest(t, display, func([]byte) {})
	rect := edge.Rect()
	if exists, viewable, overrideRedirect := windowAttributes(t, observer, edge.Window()); !exists || !viewable || !overrideRedirect {
		t.Fatalf("edge window exists %v, viewable %v, override-redirect %v; want all three", exists, viewable, overrideRedirect)
	}
	if rect.Width != 40 || rect.Y != 0 || rect.X <= 0 || rect.Height <= 0 {
		t.Fatalf("edge rect = %+v, want a 40 pixel strip on the right", rect)
	}
	served := serveOneDrop(edge, 20*time.Second)
	source := openDragsOn(t, display)
	input := startFakeInput(t, display)
	input.moveTo(20, 20)
	uri := scratchFileURI(t)

	outcome := startDrop(t, source, DropOptions{URIs: []string{uri}})
	input.press(1)
	middle := rect.Height / 2
	dragAlong(t, input, [2]int{200, middle}, [2]int{rect.X - 60, middle}, [2]int{rect.X + 5, middle}, [2]int{rect.X + rect.Width/2, middle})
	input.release(1)
	report := waitForDrop(t, outcome)
	var got edgeOutcome
	select {
	case got = <-served:
	case <-time.After(10 * time.Second):
		t.Fatal("the edge did not report the drop")
	}

	if !report.Dropped || !report.Finished || !report.FinishAccepted || report.Target != edge.Window() {
		t.Errorf("source report = %+v, want a finished and accepted drop on the edge window 0x%x", report, edge.Window())
	}
	if report.Action != "XdndActionCopy" || report.FinishAction != "XdndActionCopy" {
		t.Errorf("the source saw actions %q and %q, want XdndActionCopy for both", report.Action, report.FinishAction)
	}
	if got.err != nil {
		t.Fatalf("NextDrop: %v", got.err)
	}
	drop := got.drop
	if drop.Source != source.Window() || drop.Version != xdndVersion || !drop.OffersURIList || drop.Positions < 1 {
		t.Errorf("edge drop = %+v, want version 5 from 0x%x offering text/uri-list after a position", drop, source.Window())
	}
	if drop.URIListErr != nil || drop.URIList != uri+"\r\n" {
		t.Errorf("edge read %q, %v; want %q", drop.URIList, drop.URIListErr, uri+"\r\n")
	}
	if !drop.Accepted || drop.FinishAction != "XdndActionCopy" || drop.RequestedAction != "XdndActionCopy" {
		t.Errorf("edge finished accepted %v with %q after a request for %q, want an accepted copy", drop.Accepted, drop.FinishAction, drop.RequestedAction)
	}
	lines := heard.all()
	for _, want := range []string{"XdndEnter from", "XdndPosition 1 from", "XdndDrop from", uri, "XdndFinished sent"} {
		if !slices.ContainsFunc(lines, func(line string) bool { return strings.Contains(line, want) }) {
			t.Errorf("no logged line has %q", want)
		}
	}

	if err := edge.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if exists, _, _ := windowAttributes(t, observer, edge.Window()); exists {
		t.Error("the edge window still exists after Close")
	}
}

func TestAnEdgeWithoutADropStopsAtItsTimeLimitAndIsGoneOnceClosed(t *testing.T) {
	display := startXvfb(t)
	edge, _ := openEdgeFor(t, display, EdgeOptions{Side: "top", Width: 10})
	observer := dialForTest(t, display, func([]byte) {})

	got := <-serveOneDrop(edge, 100*time.Millisecond)

	if !errors.Is(got.err, context.DeadlineExceeded) {
		t.Errorf("NextDrop = %+v, %v; want the time limit", got.drop, got.err)
	}
	if exists, viewable, _ := windowAttributes(t, observer, edge.Window()); !exists || !viewable {
		t.Fatalf("before Close the edge window exists %v, viewable %v", exists, viewable)
	}
	edge.Close()
	if exists, _, _ := windowAttributes(t, observer, edge.Window()); exists {
		t.Error("the edge window still exists after Close")
	}
}

func TestEachEdgeIsAStripAlongItsSide(t *testing.T) {
	for side, want := range map[string]Rect{
		"left":   {X: 0, Y: 0, Width: 40, Height: 768},
		"right":  {X: 984, Y: 0, Width: 40, Height: 768},
		"top":    {X: 0, Y: 0, Width: 1024, Height: 40},
		"bottom": {X: 0, Y: 728, Width: 1024, Height: 40},
	} {
		if got, err := EdgeRect(side, 40, 1024, 768); err != nil || got != want {
			t.Errorf("EdgeRect(%s) = %+v, %v; want %+v", side, got, err, want)
		}
	}
	if _, err := EdgeRect("middle", 40, 1024, 768); !errors.Is(err, ErrEdgeSide) {
		t.Errorf("a middle edge gave %v", err)
	}
	for _, width := range []int{0, -1, 385} {
		if _, err := EdgeRect("right", width, 1024, 768); !errors.Is(err, ErrEdgeWidth) {
			t.Errorf("a %d pixel strip gave %v", width, err)
		}
	}
}
