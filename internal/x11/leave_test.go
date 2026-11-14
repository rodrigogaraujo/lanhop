//go:build linux

package x11

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"
)

const (
	xtestKeyPress   = 2
	xtestKeyRelease = 3
	escapeKeycode   = 9
)

type steps struct {
	mutex sync.Mutex
	taken []string
}

func (s *steps) add(step string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.taken = append(s.taken, step)
}

func (s *steps) all() []string {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return slices.Clone(s.taken)
}

type recordedHand struct {
	steps   *steps
	onNudge func(count int)
	nudges  int
}

func (h *recordedHand) Nudge(dx, dy int) error {
	h.steps.add(fmt.Sprintf("nudge %d,%d", dx, dy))
	h.nudges++
	if h.onNudge != nil {
		h.onNudge(h.nudges)
	}
	return nil
}

func (h *recordedHand) Release() error {
	h.steps.add("release")
	return nil
}

func (h *recordedHand) Escape() error {
	h.steps.add("escape")
	return nil
}

type fakeCatch struct {
	steps     *steps
	accepted  chan struct{}
	accepting sync.Once
	over      bool
	dropped   chan EdgeDrop
	mutex     sync.Mutex
	closes    int
}

func newFakeCatch(s *steps) *fakeCatch {
	return &fakeCatch{steps: s, accepted: make(chan struct{}), over: true, dropped: make(chan EdgeDrop, 1)}
}

func (c *fakeCatch) Accepted() <-chan struct{} { return c.accepted }

func (c *fakeCatch) Over() bool {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.over
}

func (c *fakeCatch) Dropped() <-chan EdgeDrop { return c.dropped }

func (c *fakeCatch) Close() error {
	c.mutex.Lock()
	c.closes++
	c.mutex.Unlock()
	c.steps.add("close")
	return nil
}

func (c *fakeCatch) accept() {
	c.accepting.Do(func() { close(c.accepted) })
}

func (c *fakeCatch) timesClosed() int {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.closes
}

func catchingWith(s *steps, catch *fakeCatch) func() (edgeCatcher, error) {
	return func() (edgeCatcher, error) {
		s.add("catch")
		return catch, nil
	}
}

var rightEdge = stripAtTheEdge{strip: Rect{X: 1278, Y: 372, Width: 2, Height: 256}, dx: -1}

func TestADragLeavesThroughTheCatcherTheNudgeTheAcceptanceAndThenTheRelease(t *testing.T) {
	s := &steps{}
	catch := newFakeCatch(s)
	hand := &recordedHand{steps: s, onNudge: func(count int) {
		if count == 2 {
			catch.accept()
		}
	}}
	catch.dropped <- EdgeDrop{Paths: arrivedPaths}

	paths, err := endAtEdge(context.Background(), catchingWith(s, catch), hand, rightEdge, time.Second)

	if err != nil || !slices.Equal(paths, arrivedPaths) {
		t.Errorf("endAtEdge = %q, %v, want the dragged paths", paths, err)
	}
	if got, want := s.all(), []string{"catch", "nudge -1,0", "nudge 1,0", "release", "close"}; !slices.Equal(got, want) {
		t.Errorf("steps %q, want %q", got, want)
	}
}

func TestADragThatNeverReachesTheCatcherIsEscapedAndReleasedWithoutTheCatcher(t *testing.T) {
	s := &steps{}
	catch := newFakeCatch(s)

	_, err := endAtEdge(context.Background(), catchingWith(s, catch), &recordedHand{steps: s}, rightEdge, 20*time.Millisecond)

	if !errors.Is(err, ErrDragStayed) {
		t.Errorf("endAtEdge = %v, want %v", err, ErrDragStayed)
	}
	if got, want := s.all(), []string{"catch", "nudge -1,0", "nudge 1,0", "escape", "release", "close"}; !slices.Equal(got, want) {
		t.Errorf("steps %q, want %q", got, want)
	}
}

func TestADragThatWasAcceptedAndLeftTheCatcherIsNotReleasedOverIt(t *testing.T) {
	s := &steps{}
	catch := newFakeCatch(s)
	catch.over = false
	catch.accept()

	_, err := endAtEdge(context.Background(), catchingWith(s, catch), &recordedHand{steps: s}, rightEdge, time.Second)

	if !errors.Is(err, ErrDragStayed) {
		t.Errorf("endAtEdge = %v, want %v", err, ErrDragStayed)
	}
	if got, want := s.all(), []string{"catch", "nudge -1,0", "nudge 1,0", "escape", "release", "close"}; !slices.Equal(got, want) {
		t.Errorf("steps %q, want %q", got, want)
	}
}

func TestACatcherThatCannotOpenLeavesTheDragEscapedAndReleased(t *testing.T) {
	s := &steps{}
	refused := errors.New("no window")

	_, err := endAtEdge(context.Background(), func() (edgeCatcher, error) { return nil, refused }, &recordedHand{steps: s}, rightEdge, time.Second)

	if !errors.Is(err, ErrDragStayed) || !errors.Is(err, refused) {
		t.Errorf("endAtEdge = %v, want %v and %v", err, ErrDragStayed, refused)
	}
	if got, want := s.all(), []string{"escape", "release"}; !slices.Equal(got, want) {
		t.Errorf("steps %q, want %q", got, want)
	}
}

func TestADropThatComesLateIsClosedOnlyOnceItCame(t *testing.T) {
	s := &steps{}
	catch := newFakeCatch(s)
	catch.accept()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	_, err := endAtEdge(ctx, catchingWith(s, catch), &recordedHand{steps: s}, rightEdge, time.Second)

	if !errors.Is(err, ErrEdgeDropLate) {
		t.Errorf("endAtEdge = %v, want %v", err, ErrEdgeDropLate)
	}
	if catch.timesClosed() != 0 {
		t.Fatal("the catcher closed before its drop came")
	}
	catch.dropped <- EdgeDrop{Paths: arrivedPaths}
	close(catch.dropped)
	for deadline := time.Now().Add(5 * time.Second); catch.timesClosed() == 0; time.Sleep(2 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the catcher was not closed after its late drop")
		}
	}
}

func TestTheStripAtTheEdgeHoldsTheCursorAndThePixelInwardOfIt(t *testing.T) {
	cases := []struct {
		name   string
		x, y   int
		want   Rect
		dx, dy int
	}{
		{"right edge", 1279, 500, Rect{X: 1278, Y: 372, Width: 2, Height: 256}, -1, 0},
		{"left edge", 0, 500, Rect{X: 0, Y: 372, Width: 2, Height: 256}, 1, 0},
		{"top edge", 600, 0, Rect{X: 472, Y: 0, Width: 256, Height: 2}, 0, 1},
		{"bottom edge", 600, 1023, Rect{X: 472, Y: 1022, Width: 256, Height: 2}, 0, -1},
		{"bottom edge near a corner", 2, 1023, Rect{X: 0, Y: 1022, Width: 130, Height: 2}, 0, -1},
		{"two pixels from the right edge", 1277, 500, Rect{X: 1276, Y: 372, Width: 4, Height: 256}, -1, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := edgeStripAt(c.x, c.y, 1280, 1024)
			if got.strip != c.want || got.dx != c.dx || got.dy != c.dy {
				t.Errorf("edgeStripAt = %+v, want %+v nudged %d,%d", got, c.want, c.dx, c.dy)
			}
			inside := func(x, y int) bool {
				return x >= got.strip.X && x < got.strip.X+got.strip.Width && y >= got.strip.Y && y < got.strip.Y+got.strip.Height
			}
			if !inside(c.x, c.y) || !inside(c.x+c.dx, c.y+c.dy) || !got.strip.fitsTheProtocol() {
				t.Errorf("the strip %+v does not hold the cursor and the nudge, or the protocol refuses it", got.strip)
			}
		})
	}
}

type xtestHand struct {
	t     *testing.T
	input *fakeInput
	steps *steps
}

func (h xtestHand) Nudge(dx, dy int) error {
	h.steps.add(fmt.Sprintf("nudge %d,%d", dx, dy))
	h.input.moveBy(h.t, dx, dy)
	time.Sleep(10 * time.Millisecond)
	return nil
}

func (h xtestHand) Release() error {
	h.steps.add("release")
	h.input.release(h.t, 1)
	return nil
}

func (h xtestHand) Escape() error {
	h.steps.add("escape")
	h.input.fake(h.t, xtestKeyPress, escapeKeycode, atomNone, 0, 0)
	h.input.fake(h.t, xtestKeyRelease, escapeKeycode, atomNone, 0, 0)
	return nil
}

func screenOf(t *testing.T, drags *Drags) windowArea {
	t.Helper()
	screen, err := drags.areaOf(drags.c.root)
	if err != nil {
		t.Fatalf("the size of the screen: %v", err)
	}
	return screen
}

func TestADragThatLeavesThisMachineIsDroppedOnTheEdgeWithCopyThroughTheVirtualMouse(t *testing.T) {
	display := startXvfb(t)
	edge := openDragsOn(t, display)
	source := openDragsOn(t, display)
	input := startFakeInput(t, display)
	screen := screenOf(t, edge)
	right, middle := screen.width-1, screen.height/2
	input.moveTo(t, right-60, middle)
	drag, err := source.Start()
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(drag.Cancel)
	input.press(t, 1)
	if err := drag.FilesArrived(arrivedPaths); err != nil {
		t.Fatalf("FilesArrived: %v", err)
	}
	for _, x := range []int{right - 40, right - 20, right} {
		input.moveTo(t, x, middle)
		time.Sleep(10 * time.Millisecond)
	}
	s := &steps{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	paths, err := edge.EndDragAtEdge(ctx, xtestHand{t: t, input: input, steps: s})

	if err != nil || !slices.Equal(paths, arrivedPaths) {
		t.Errorf("EndDragAtEdge = %q, %v, want the dragged paths", paths, err)
	}
	if err := waitForDrag(t, drag); err != nil {
		t.Errorf("the source drag ended with %v, want a drop that the edge took", err)
	}
	if got, want := s.all(), []string{"nudge -1,0", "nudge 1,0", "release"}; !slices.Equal(got, want) {
		t.Errorf("steps %q, want %q", got, want)
	}
	if top := topWindowAt(t, edge, right, middle); top != atomNone {
		t.Errorf("a window 0x%x is still at the edge", top)
	}
}

func TestADragThatIsNoXdndDragIsEscapedAndReleasedAtTheEdge(t *testing.T) {
	display := startXvfb(t)
	edge := openDragsOn(t, display)
	input := startFakeInput(t, display)
	screen := screenOf(t, edge)
	middle := screen.height / 2
	input.moveTo(t, 0, middle)
	input.press(t, 1)
	s := &steps{}

	_, err := edge.EndDragAtEdge(context.Background(), xtestHand{t: t, input: input, steps: s})

	if !errors.Is(err, ErrDragStayed) {
		t.Errorf("EndDragAtEdge = %v, want %v", err, ErrDragStayed)
	}
	if got, want := s.all(), []string{"nudge 1,0", "nudge -1,0", "escape", "release"}; !slices.Equal(got, want) {
		t.Errorf("steps %q, want %q", got, want)
	}
	if top := topWindowAt(t, edge, 0, middle); top != atomNone {
		t.Errorf("a window 0x%x is still at the edge", top)
	}
	if !pointerIsFree(t, display) {
		t.Error("the pointer is held after the drag stayed")
	}
}
