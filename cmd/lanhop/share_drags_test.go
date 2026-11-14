package main

import (
	"context"
	"errors"
	"fmt"
	"io"
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

func (f *fakeCentralDrops) DragOver() {
	f.calls.note("central: drag over")
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
}

func newDragsHarness() *dragsHarness {
	calls := &dragCalls{}
	h := &dragsHarness{calls: calls, central: &fakeCentralDrops{calls: calls}, output: &screen{}}
	endHere := func() error {
		calls.note(sourceDragEndedHere)
		if failure := h.endFail.Load(); failure != nil {
			return *failure
		}
		return nil
	}
	h.dragsOfTheCentral = newDragsOfTheCentral(theCentral, nil, endHere, h.output)
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

			if err := h.crossing(c.crossing); err != nil {
				t.Errorf("the crossing was refused: %v", err)
			}

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
	h.DragGone()

	h.expectCalls(t, "a drag that could not be ended here",
		sourceDragEndedHere,
		"sharer: drag cancelled",
		"central: drag cancelled",
		"sharer: the source drag ended on studio",
		"central: drag gone",
	)
	if !strings.Contains(h.output.String(), "is cancelled, and nothing is dropped, because lanhop could not end it here: posting refused") {
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

func TestACrossingIsRefusedWhenItsOriginCannotBeToldThatTheDragLeaves(t *testing.T) {
	h := newDragsHarness()
	h.central.leaves = clipshare.ErrNoFileDrag

	if err := h.crossing(crossingOf("desk", "desk", "laptop")); !errors.Is(err, clipshare.ErrNoFileDrag) {
		t.Errorf("the crossing gave %v, want it refused with the failure to tell desk", err)
	}

	h.expectCalls(t, "a crossing whose origin could not be told", "central: desk is told its drag leaves")
	if !strings.Contains(h.output.String(), `The file drag on "desk" stays there, because lanhop could not tell it that the drag leaves`) {
		t.Errorf("the log says %q, want the drag that stays on desk", h.output.String())
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

func TestADragOnTheCentralStartsInTheSharerOnlyOnceItsFilesAreCounted(t *testing.T) {
	h := newDragsHarness()

	h.DragSeen([]string{"/a", "/b"}, edge.Bottom)
	h.expectCalls(t, "a drag seen and not counted yet", `central: drag seen ["/a" "/b"]`)

	h.ownDragCounted(2)
	h.DragGone()
	h.expectCalls(t, "the count of that drag and its end",
		"sharer: drag started {Origin:studio Files:2 Dock:bottom}",
		"sharer: the source drag ended on studio",
		"central: drag gone",
	)

	h.DragSeen([]string{"/too-many"}, edge.Left)
	h.DragGone()
	h.expectCalls(t, "a drag whose count never came",
		`central: drag seen ["/too-many"]`,
		"sharer: the source drag ended on studio",
		"central: drag gone",
	)

	h.DragSeen([]string{"/a"}, 0)
	h.ownDragCounted(1)
	h.entered(crossingOf(theCentral, theCentral, "desk"))
	h.DragGone()
	h.dragEnded(sharer.Drag{Origin: theCentral, Files: 1})
	h.expectCalls(t, "a drag that left the central and was dropped",
		`central: drag seen ["/a"]`,
		"sharer: drag started {Origin:studio Files:1 Dock:unknown}",
		sourceDragEndedHere,
		"central: drag crossed from studio to desk",
		"sharer: the source drag ended on studio",
		"central: drag gone",
		"central: drag over",
	)
}

func TestTheDragsOfSideMachinesAndTheirDocksReachTheSharer(t *testing.T) {
	h := newDragsHarness()

	h.sideDragStarted(clipshare.SideDrag{Origin: "desk", Files: 3, Dock: edge.Left})
	h.sideDragStarted(clipshare.SideDrag{Origin: "laptop", Files: 1})
	h.sideDragEnded("desk")
	h.sideDock("tablet", edge.Right)
	h.sideDock("tablet", 0)

	h.expectCalls(t, "drags and docks of side machines",
		"sharer: drag started {Origin:desk Files:3 Dock:left}",
		"sharer: the dock of desk is at left",
		"sharer: drag started {Origin:laptop Files:1 Dock:unknown}",
		"sharer: the source drag ended on desk",
		"sharer: the dock of tablet is at right",
		"sharer: the dock of tablet is at unknown",
	)
}

func TestADragThatStayedOnTheSideMachineItLeftIsCancelled(t *testing.T) {
	h := newDragsHarness()

	h.sideDragStayed("desk")

	h.expectCalls(t, "a drag that stayed on desk", "sharer: drag cancelled", "central: drag cancelled")
	if !strings.Contains(h.output.String(), `The file drag that left "desk" stayed there`) {
		t.Errorf("the log says %q, want the drag that stayed", h.output.String())
	}
}

func TestTheDockOfTheCentralIsToldOnlyWhenItIsKnown(t *testing.T) {
	h := newDragsHarness()

	h.DockIs(edge.Bottom)
	h.DockIs(0)
	h.DockIs(edge.Top)

	h.expectCalls(t, "the docks read on the central",
		"sharer: the dock of studio is at bottom",
		"sharer: the dock of studio is at top",
	)
}

func TestTheWatchOfDragsOnTheCentralFollowsWhetherDragsAreAllowed(t *testing.T) {
	var allowed atomic.Bool
	told := make(chan bool, 64)
	done, stop := context.WithCancel(context.Background())
	followed := make(chan struct{})
	go func() {
		defer close(followed)
		followTheDragSwitch(done, allowed.Load, func(on bool) { told <- on }, time.Millisecond)
	}()

	if on := await(t, told); on {
		t.Error("the watch was wanted while drags were not allowed")
	}
	allowed.Store(true)
	for deadline := time.Now().Add(testPatience); !await(t, told); {
		if time.Now().After(deadline) {
			t.Fatal("the watch was never wanted once drags were allowed")
		}
	}
	stop()
	await(t, followed)
}

func TestNoDragOfTheCentralIsWatchedWithoutAClipboardToCarryIt(t *testing.T) {
	drags := newDragsOfTheCentral(theCentral, nil, func() error { return nil }, &screen{})

	drags.watch(context.Background())()
}

func TestEndingASourceDragUnderAShieldOnTheCentralSaysWhetherItEnded(t *testing.T) {
	missed := func(context.Context) (io.Closer, error) { return nil, errors.New("the window did not take the mouse") }
	log := &screen{}

	if err := endTheSourceDragUnderAShield(context.Background(), missed, func(io.Closer) error { return nil }, nil, log, nil); err != nil {
		t.Errorf("a source drag ended after the shield missed gave %v, want no error", err)
	}
	if err := endTheSourceDragUnderAShield(context.Background(), missed, func(io.Closer) error { return errPostingRefused }, nil, log, nil); !errors.Is(err, errPostingRefused) {
		t.Errorf("a source drag that could not be ended gave %v, want %v", err, errPostingRefused)
	}
	if !strings.Contains(log.String(), "and ends it anyway") {
		t.Errorf("the log says %q, want the missed shield", log.String())
	}
}

type hookedCentral struct {
	*fakeCentralDrops
	counted func(files int)
	started func(clipshare.SideDrag)
	ended   func(origin string)
	stayed  func(origin string)
	dock    func(origin string, dock edge.Side)
}

func (c *hookedCentral) OnOwnDragCounted(counted func(files int)) {
	c.counted = counted
}

func (c *hookedCentral) OnSideDrags(started func(clipshare.SideDrag), ended func(origin string)) {
	c.started, c.ended = started, ended
}

func (c *hookedCentral) OnSideDragStayed(stayed func(origin string)) {
	c.stayed = stayed
}

func (c *hookedCentral) OnSideDock(dock func(origin string, dock edge.Side)) {
	c.dock = dock
}

type hookedSharer struct {
	fakeSharerDrags
	crossing   func(sharer.DragCrossing) error
	entered    func(sharer.DragCrossing)
	ended      func(sharer.Drag)
	escape     func(bool)
	leftButton func(bool)
}

func (s *hookedSharer) OnDragCrossing(crossing func(sharer.DragCrossing) error) {
	s.crossing = crossing
}

func (s *hookedSharer) OnDragEntered(entered func(sharer.DragCrossing)) {
	s.entered = entered
}

func (s *hookedSharer) OnDragEnded(ended func(sharer.Drag)) {
	s.ended = ended
}

func (s *hookedSharer) OnEscape(pressed func(bool)) {
	s.escape = pressed
}

func (s *hookedSharer) OnLeftButton(changed func(bool)) {
	s.leftButton = changed
}

func TestWiringTheCentralSetsEveryHookOfTheClipboardAndTheSharer(t *testing.T) {
	calls := &dragCalls{}
	central := &hookedCentral{fakeCentralDrops: &fakeCentralDrops{calls: calls}}
	machine := &hookedSharer{fakeSharerDrags: fakeSharerDrags{calls: calls}}
	drags := newDragsOfTheCentral(theCentral, nil, func() error { return nil }, &screen{})
	drags.reset = func() { calls.note(localDragsReset) }

	drags.wire(machine, central, func() bool { return true })
	central.counted(2)
	central.started(clipshare.SideDrag{Origin: "desk", Files: 1})
	central.ended("desk")
	central.stayed("desk")
	central.dock("desk", edge.Top)
	machine.crossing(crossingOf("desk", "laptop", "desk"))
	machine.entered(crossingOf("desk", "desk", "laptop"))
	machine.ended(sharer.Drag{Origin: "desk"})
	machine.escape(false)
	machine.leftButton(true)

	if got, want := calls.take(), []string{
		"sharer: drag started {Origin:studio Files:2 Dock:unknown}",
		"sharer: drag started {Origin:desk Files:1 Dock:unknown}",
		"sharer: the source drag ended on desk",
		"sharer: drag cancelled",
		"central: drag cancelled",
		"sharer: the dock of desk is at top",
		"central: drag cancelled",
		"central: drag crossed from desk to laptop",
		"central: drag over",
		"central: drag cancelled",
		localDragsReset,
	}; !slices.Equal(got, want) {
		t.Errorf("the hooks led to:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
