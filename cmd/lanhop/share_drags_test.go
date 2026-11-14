package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
	"github.com/rodrigogaraujo/lanhop/internal/clipshare"
	"github.com/rodrigogaraujo/lanhop/internal/edge"
	"github.com/rodrigogaraujo/lanhop/internal/sharer"
)

const theCentral = "studio"

var (
	sourceDragEndedHere = "this machine: the source drag is ended"
	localDragsReset     = "this machine: the watch of drags is reset"
	errPostingRefused   = errors.New("posting refused")
)

type dragCalls struct {
	mutex sync.Mutex
	calls []string
}

func (c *dragCalls) note(format string, args ...any) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.calls = append(c.calls, fmt.Sprintf(format, args...))
}

func (c *dragCalls) take() []string {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	taken := c.calls
	c.calls = nil
	return taken
}

type fakeCentralDrops struct {
	calls   *dragCalls
	crossed error
	leaves  error
}

func (f *fakeCentralDrops) DragSeen(paths []string) {
	f.calls.note("central: drag seen %q", paths)
}

func (f *fakeCentralDrops) DragGone() {
	f.calls.note("central: drag gone")
}

func (f *fakeCentralDrops) DragCrossed(origin, to string) error {
	f.calls.note("central: drag crossed from %s to %s", origin, to)
	return f.crossed
}

func (f *fakeCentralDrops) DragCancelled() {
	f.calls.note("central: drag cancelled")
}

func (f *fakeCentralDrops) DragLeaves(origin string) error {
	f.calls.note("central: %s is told its drag leaves", origin)
	return f.leaves
}

type fakeSharerDrags struct {
	calls *dragCalls
}

func (f fakeSharerDrags) DragStarted(drag sharer.Drag) {
	f.calls.note("sharer: drag started %+v", drag)
}

func (f fakeSharerDrags) DragCancelled() {
	f.calls.note("sharer: drag cancelled")
}

func (f fakeSharerDrags) SourceDragEnded(origin string) {
	f.calls.note("sharer: the source drag ended on %s", origin)
}

func (f fakeSharerDrags) SetDock(machine string, dock edge.Side) {
	f.calls.note("sharer: the dock of %s is at %v", machine, dock)
}

type dragsHarness struct {
	*dragsOfTheCentral
	calls   *dragCalls
	central *fakeCentralDrops
	output  *screen
	endFail atomic.Pointer[error]
	dockAt  atomic.Int32
}

func newDragsHarness() *dragsHarness {
	calls := &dragCalls{}
	h := &dragsHarness{calls: calls, central: &fakeCentralDrops{calls: calls}, output: &screen{}}
	h.dockAt.Store(int32(edge.Bottom))
	endHere := func() error {
		calls.note(sourceDragEndedHere)
		if failure := h.endFail.Load(); failure != nil {
			return *failure
		}
		return nil
	}
	dock := func() edge.Side { return edge.Side(h.dockAt.Load()) }
	h.dragsOfTheCentral = newDragsOfTheCentral(theCentral, nil, dock, endHere, h.output)
	h.dragsOfTheCentral.central = h.central
	h.dragsOfTheCentral.machine = fakeSharerDrags{calls: calls}
	h.dragsOfTheCentral.reset = func() { calls.note(localDragsReset) }
	return h
}

func (h *dragsHarness) expectCalls(t *testing.T, after string, want ...string) {
	t.Helper()
	if got := h.calls.take(); !slices.Equal(got, want) {
		t.Errorf("after %s the calls were:\n%s\nwant:\n%s", after, strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func crossingOf(origin, from, to string) sharer.DragCrossing {
	return sharer.DragCrossing{Origin: origin, From: from, To: to, Files: 2, Button: 0x110}
}

func TestOnlyTheSideMachineThatADragLeavesIsToldToEndIt(t *testing.T) {
	cases := []struct {
		name     string
		crossing sharer.DragCrossing
		want     []string
	}{
		{"leaving the side machine it started on", crossingOf("desk", "desk", "laptop"), []string{"central: desk is told its drag leaves"}},
		{"leaving it for the central", crossingOf("desk", "desk", theCentral), []string{"central: desk is told its drag leaves"}},
		{"on from a side machine it entered", crossingOf("desk", "laptop", "tablet"), nil},
		{"leaving the central it started on", crossingOf(theCentral, theCentral, "desk"), nil},
		{"on from the central it entered", crossingOf("desk", theCentral, "laptop"), nil},
		{"back to the side machine it started on", crossingOf("desk", "laptop", "desk"), []string{"central: drag cancelled"}},
		{"back to the central it started on", crossingOf(theCentral, "desk", theCentral), []string{"central: drag cancelled"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newDragsHarness()

			h.crossing(c.crossing)

			h.expectCalls(t, "the crossing", c.want...)
		})
	}
}

func TestADragEndsOnTheCentralOnlyOnceItEnteredTheNextMachine(t *testing.T) {
	cases := []struct {
		name     string
		crossing sharer.DragCrossing
		want     []string
	}{
		{"leaving the central it started on", crossingOf(theCentral, theCentral, "desk"), []string{sourceDragEndedHere, "central: drag crossed from studio to desk"}},
		{"leaving the side machine it started on", crossingOf("desk", "desk", "laptop"), []string{"central: drag crossed from desk to laptop"}},
		{"on from a side machine, started on the central", crossingOf(theCentral, "desk", "laptop"), []string{"central: drag crossed from studio to laptop"}},
		{"into the central, started on a side machine", crossingOf("desk", "desk", theCentral), []string{"central: drag crossed from desk to studio"}},
		{"back to the side machine it started on", crossingOf("desk", "laptop", "desk"), nil},
		{"back to the central it started on", crossingOf(theCentral, "desk", theCentral), nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newDragsHarness()

			h.crossing(c.crossing)
			h.calls.take()
			h.entered(c.crossing)

			h.expectCalls(t, "the entry", c.want...)
		})
	}
}

func TestADragThatCannotBeEndedOnTheCentralCrossesNoFurther(t *testing.T) {
	h := newDragsHarness()
	failure := error(errPostingRefused)
	h.endFail.Store(&failure)
	leaving := crossingOf(theCentral, theCentral, "desk")

	h.entered(leaving)
	h.localDrag(localDrag{})

	h.expectCalls(t, "a drag that could not be ended here",
		sourceDragEndedHere,
		"sharer: drag cancelled",
		"central: drag cancelled",
		"sharer: the source drag ended on studio",
		"central: drag gone",
	)
	if !strings.Contains(h.output.String(), "lanhop could not end it here: posting refused") {
		t.Errorf("the log says %q, want why the drag did not cross", h.output.String())
	}
}

func TestAMachineThatTakesNoDroppedFilesIsLoggedOnceAndTheDragGoesOn(t *testing.T) {
	h := newDragsHarness()
	h.central.crossed = fmt.Errorf("wrapped: %w", clipboard.ErrDropsNotTaken)

	for _, to := range []string{"desk", "desk", "laptop", "desk"} {
		h.entered(crossingOf(theCentral, "tablet", to))
	}

	for _, call := range h.calls.take() {
		if strings.HasPrefix(call, "sharer:") {
			t.Errorf("a machine that takes no dropped files led to %q", call)
		}
	}
	for machine, want := range map[string]int{"desk": 1, "laptop": 1} {
		line := fmt.Sprintf("A file dragged to %q is not dropped there, because that machine does not take dragged files now.\n", machine)
		if got := strings.Count(h.output.String(), line); got != want {
			t.Errorf("the log says %d times that %s takes no dropped files, want %d:\n%s", got, machine, want, h.output.String())
		}
	}
}

func TestAnyOtherFailureOfTheCrossingCancelsTheDrag(t *testing.T) {
	for _, failure := range []error{clipshare.ErrNoFileDrag, clipboard.ErrFilesNotShared, errors.New("clipshare: clipboard sharing has stopped")} {
		h := newDragsHarness()
		h.central.crossed = failure

		h.entered(crossingOf("desk", "desk", "laptop"))

		h.expectCalls(t, fmt.Sprintf("a crossing that failed with %v", failure),
			"central: drag crossed from desk to laptop",
			"sharer: drag cancelled",
		)
		if !strings.Contains(h.output.String(), `The file dragged to "laptop" is not dropped there: `+failure.Error()) {
			t.Errorf("the log says %q, want the failure", h.output.String())
		}
	}
}

func TestAFailureToTellTheOriginIsLoggedAndTheCrossingGoesOn(t *testing.T) {
	h := newDragsHarness()
	h.central.leaves = clipshare.ErrNoFileDrag
	leaving := crossingOf("desk", "desk", "laptop")

	h.crossing(leaving)
	h.entered(leaving)

	h.expectCalls(t, "a crossing whose origin could not be told",
		"central: desk is told its drag leaves",
		"central: drag crossed from desk to laptop",
	)
	if !strings.Contains(h.output.String(), `lanhop could not tell "desk" to end the file drag that left it`) {
		t.Errorf("the log says %q, want the failure to tell desk", h.output.String())
	}
}

func TestEscapeOnTheCentralCancelsADropNotTakenAndADragInProgress(t *testing.T) {
	h := newDragsHarness()

	h.escape(false)
	h.expectCalls(t, "Escape outside a drag", "central: drag cancelled")

	h.escape(true)
	h.expectCalls(t, "Escape during a drag", "central: drag cancelled", localDragsReset, "sharer: drag cancelled")
}

func TestEveryPressAndReleaseOfTheLeftButtonResetsTheWatchOfDrags(t *testing.T) {
	h := newDragsHarness()

	h.leftButton(true)
	h.leftButton(false)

	h.expectCalls(t, "a click", localDragsReset, localDragsReset)
}

func TestADragOnTheCentralIsToldToTheClipboardAndTheSharer(t *testing.T) {
	h := newDragsHarness()

	h.localDrag(localDrag{Started: true, Paths: []string{"/a", "/b"}})
	h.localDrag(localDrag{})
	h.expectCalls(t, "a drag that started and ended on the central",
		`central: drag seen ["/a" "/b"]`,
		"sharer: drag started {Origin:studio Files:2 Dock:bottom}",
		"sharer: the source drag ended on studio",
		"central: drag gone",
	)

	h.localDrag(localDrag{Started: true, Paths: []string{"/a"}})
	h.entered(crossingOf(theCentral, theCentral, "desk"))
	h.localDrag(localDrag{})
	h.expectCalls(t, "a drag that left the central",
		`central: drag seen ["/a"]`,
		"sharer: drag started {Origin:studio Files:1 Dock:bottom}",
		sourceDragEndedHere,
		"central: drag crossed from studio to desk",
		"sharer: the source drag ended on studio",
	)

	h.localDrag(localDrag{Started: true, Paths: []string{"/c"}})
	h.localDrag(localDrag{})
	h.expectCalls(t, "the next drag, which stayed on the central",
		`central: drag seen ["/c"]`,
		"sharer: drag started {Origin:studio Files:1 Dock:bottom}",
		"sharer: the source drag ended on studio",
		"central: drag gone",
	)
}

func TestTheDragsOfSideMachinesAndTheirDocksReachTheSharer(t *testing.T) {
	h := newDragsHarness()

	h.sideDragStarted(clipshare.SideDrag{Origin: "desk", Files: 3, Dock: edge.Left})
	h.sideDragStarted(clipshare.SideDrag{Origin: "laptop", Files: 1})
	h.sideDragEnded("desk")
	h.dockOf("tablet", edge.Right)
	h.dockOf("tablet", 0)

	h.expectCalls(t, "drags and docks of side machines",
		"sharer: drag started {Origin:desk Files:3 Dock:left}",
		"sharer: the dock of desk is at left",
		"sharer: drag started {Origin:laptop Files:1 Dock:unknown}",
		"sharer: the source drag ended on desk",
		"sharer: the dock of tablet is at right",
	)
}

func TestTheDockOfTheCentralIsToldAtOnceAndKeptFresh(t *testing.T) {
	h := newDragsHarness()
	done, stop := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		h.keepTheDockFresh(done, time.Millisecond)
	}()
	defer func() {
		stop()
		<-stopped
	}()

	waitForCall(t, h.calls, "sharer: the dock of studio is at bottom")
	h.dockAt.Store(0)
	time.Sleep(20 * time.Millisecond)
	h.calls.take()
	time.Sleep(20 * time.Millisecond)
	if got := h.calls.take(); len(got) > 0 {
		t.Errorf("a dock that cannot be read was told: %q", got)
	}
	h.dockAt.Store(int32(edge.Left))
	waitForCall(t, h.calls, "sharer: the dock of studio is at left")
}

func TestTheDragsOfTheCentralAreFollowedUntilTheirWatchEnds(t *testing.T) {
	h := newDragsHarness()
	drags := make(chan localDrag)
	followed := make(chan struct{})
	go func() {
		defer close(followed)
		h.follow(context.Background(), drags)
	}()

	drags <- localDrag{Started: true, Paths: []string{"/a"}}
	drags <- localDrag{}
	close(drags)
	await(t, followed)

	h.expectCalls(t, "a drag seen by the watch",
		`central: drag seen ["/a"]`,
		"sharer: drag started {Origin:studio Files:1 Dock:bottom}",
		"sharer: the source drag ended on studio",
		"central: drag gone",
	)
}

func TestNoDragOfTheCentralIsWatchedWithoutAClipboardToCarryIt(t *testing.T) {
	drags := newDragsOfTheCentral(theCentral, nil, func() edge.Side { return 0 }, func() error { return nil }, &screen{})

	drags.watch(context.Background())()
}

func waitForCall(t *testing.T, calls *dragCalls, wanted string) {
	t.Helper()
	var seen []string
	for deadline := time.Now().Add(testPatience); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		seen = append(seen, calls.take()...)
		if slices.Contains(seen, wanted) {
			return
		}
	}
	t.Fatalf("never saw %q, saw %q", wanted, seen)
}
