package macos

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

type dropScreen struct {
	mu     sync.Mutex
	cursor Point
	posted []MouseEvent
	onPost func(MouseEvent)
}

func (s *dropScreen) PostMouse(mouse MouseEvent) {
	s.mu.Lock()
	s.posted = append(s.posted, mouse)
	s.cursor = mouse.At
	react := s.onPost
	s.mu.Unlock()
	if react != nil {
		react(mouse)
	}
}

func (s *dropScreen) Cursor() Point {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cursor
}

func (s *dropScreen) moveTo(at Point) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cursor = at
}

func (s *dropScreen) events() []MouseEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.posted)
}

var (
	macBookDisplay   = Rect{X: 0, Y: 0, Width: 1440, Height: 900}
	displayOnTheLeft = Rect{X: -1920, Y: 0, Width: 1920, Height: 1080}
	leftEdge         = Point{X: 0, Y: 400}
)

var quickTiming = dragTiming{
	showLimit:  50 * time.Millisecond,
	showPoll:   time.Millisecond,
	downLimit:  50 * time.Millisecond,
	firstStep:  time.Millisecond,
	nextStep:   time.Millisecond,
	beginLimit: 50 * time.Millisecond,
}

func postedDown(at Point) MouseEvent {
	return MouseEvent{Action: MouseDown, At: at, Clicks: 1}
}

func postedDrag(at Point, dx int32) MouseEvent {
	return MouseEvent{Action: MouseDragged, At: at, Clicks: 1, DX: dx}
}

func postedUp(at Point) MouseEvent {
	return MouseEvent{Action: MouseUp, At: at, Clicks: 1}
}

func stepsRightOf(home Point) []MouseEvent {
	var steps []MouseEvent
	for i := 1; i <= dragSteps; i++ {
		steps = append(steps, postedDrag(Point{X: home.X + float64(3*i), Y: home.Y}, 3))
	}
	return steps
}

func expectEvents(t *testing.T, got []MouseEvent, want ...MouseEvent) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("posted %+v,\nwant %+v", got, want)
	}
}

func everyDownHasItsUp(events []MouseEvent) bool {
	held := 0
	for _, event := range events {
		switch event.Action {
		case MouseDown:
			held++
		case MouseUp:
			held--
		}
	}
	return held == 0
}

type appKitAnswers struct {
	window   windowState
	seesDown bool
	begins   bool
}

type startUnderTest struct {
	screen   *dropScreen
	button   *heldDown
	downSeen *latch
	began    *latch
	start    dragStart
}

func startOnTheLeftEdge(answers appKitAnswers) *startUnderTest {
	screen := &dropScreen{cursor: leftEdge}
	s := &startUnderTest{screen: screen, button: newHeldDown(screen, leftEdge), downSeen: newLatch(), began: newLatch()}
	screen.onPost = func(mouse MouseEvent) {
		if mouse.Action == MouseDown && answers.seesDown {
			s.downSeen.fire()
		}
		if mouse.Action == MouseDragged && answers.begins {
			s.began.fire()
		}
	}
	s.start = dragStart{
		plan:     planDrag(leftEdge, []Rect{macBookDisplay}),
		button:   s.button,
		window:   func() windowState { return answers.window },
		downSeen: s.downSeen.done,
		began:    s.began.done,
		timing:   quickTiming,
	}
	return s
}

func TestOperationsAreNamedByTheirAppKitBits(t *testing.T) {
	cases := []struct {
		operation Operation
		name      string
		accepted  bool
	}{
		{OperationNone, "none", false},
		{OperationCopy, "copy", true},
		{OperationCopy | OperationMove, "copy+move", true},
		{OperationLink, "link", false},
		{OperationGeneric | 128, "generic+0x80", false},
	}
	for _, c := range cases {
		if got := c.operation.String(); got != c.name {
			t.Errorf("Operation(%d).String() = %q, want %q", uint64(c.operation), got, c.name)
		}
		if got := c.operation.Accepted(); got != c.accepted {
			t.Errorf("Operation(%d).Accepted() = %v, want %v", uint64(c.operation), got, c.accepted)
		}
	}
}

func TestAPromisedFileNeedsAPlainNameThatMacOSCanCreate(t *testing.T) {
	for _, name := range []string{"report.pdf", "Read me", "notes:draft.txt", ".hidden", "relatório.txt", strings.Repeat("a", 255)} {
		if err := checkPromisedFiles([]PromisedFile{{Name: name}}, 1); err != nil {
			t.Errorf("%q was refused: %v", name, err)
		}
	}
	for _, name := range []string{"", ".", "..", "a/b", "/etc", "../up", "nul\x00byte", "\xff\xfe", strings.Repeat("a", 256)} {
		if err := checkPromisedFiles([]PromisedFile{{Name: name}}, 1); !errors.Is(err, ErrPromisedName) {
			t.Errorf("%q gave %v, want ErrPromisedName", name, err)
		}
	}
}

func TestADropHoldsAtLeastOneFileAndNoMoreThanTheLimit(t *testing.T) {
	if err := checkPromisedFiles(nil, 2); !errors.Is(err, ErrNoPromisedFiles) {
		t.Errorf("no file gave %v, want ErrNoPromisedFiles", err)
	}
	three := []PromisedFile{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	if err := checkPromisedFiles(three, 2); !errors.Is(err, ErrTooManyPromisedFiles) {
		t.Errorf("three files with a limit of two gave %v, want ErrTooManyPromisedFiles", err)
	}
	if err := checkPromisedFiles(three[:2], 2); err != nil {
		t.Errorf("two files with a limit of two gave %v", err)
	}
}

func TestAFolderIsPromisedAsAFolderAndAFileIsLeftToItsName(t *testing.T) {
	files := []PromisedFile{{Name: "photos", Folder: true}, {Name: "report.pdf"}}
	if got, want := promisedKinds(files), []string{"public.folder", ""}; !slices.Equal(got, want) {
		t.Errorf("kinds = %q, want %q", got, want)
	}
	if got, want := promisedNames(files), []string{"photos", "report.pdf"}; !slices.Equal(got, want) {
		t.Errorf("names = %q, want %q", got, want)
	}
}

func TestTheDragStepsGoIntoTheScreenAndStayInTheWindowOfLanhop(t *testing.T) {
	displays := []Rect{macBookDisplay, displayOnTheLeft}
	cases := []struct {
		cursor    Point
		direction float64
	}{
		{Point{X: 0, Y: 400}, 1},
		{Point{X: 1439, Y: 400}, -1},
		{Point{X: 700, Y: 0}, 1},
		{Point{X: -1920, Y: 500}, 1},
		{Point{X: -1, Y: 500}, -1},
	}
	for _, c := range cases {
		plan := planDrag(c.cursor, displays)
		if plan.start != c.cursor {
			t.Errorf("from %+v the drag starts at %+v", c.cursor, plan.start)
		}
		if len(plan.steps) != dragSteps {
			t.Fatalf("from %+v the drag has %d steps, want %d", c.cursor, len(plan.steps), dragSteps)
		}
		for i, step := range plan.steps {
			want := Point{X: c.cursor.X + c.direction*3*float64(i+1), Y: c.cursor.Y}
			if step != want {
				t.Errorf("from %+v step %d = %+v, want %+v", c.cursor, i, step, want)
			}
			if !within(step, c.cursor, dropWindowSide/2-1) {
				t.Errorf("from %+v step %d = %+v leaves the window of lanhop", c.cursor, i, step)
			}
		}
	}
}

func TestADragStartsWithAMouseDownInTheWindowThenSmallDraggedSteps(t *testing.T) {
	s := startOnTheLeftEdge(appKitAnswers{window: windowReady, seesDown: true, begins: true})

	if err := s.start.run(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}

	expectEvents(t, s.screen.events(), append([]MouseEvent{postedDown(leftEdge)}, stepsRightOf(leftEdge)...)...)
	if !s.button.pending() {
		t.Error("after a drag session began, the posted mouse down is not counted as held")
	}
}

func TestNothingIsPostedWhenTheWindowOfLanhopIsNotUnderTheCursor(t *testing.T) {
	cases := []struct {
		window windowState
		want   error
	}{
		{windowNotShown, ErrDropWindowNotShown},
		{windowCovered, ErrDropWindowCovered},
	}
	for _, c := range cases {
		s := startOnTheLeftEdge(appKitAnswers{window: c.window, seesDown: true, begins: true})
		if err := s.start.run(context.Background()); !errors.Is(err, c.want) {
			t.Errorf("window state %d gave %v, want %v", c.window, err, c.want)
		}
		expectEvents(t, s.screen.events())
	}
}

func TestNothingIsPostedWhenTheCursorMovedAwayFromTheWindow(t *testing.T) {
	s := startOnTheLeftEdge(appKitAnswers{window: windowReady, seesDown: true, begins: true})
	s.screen.moveTo(Point{X: leftEdge.X + 5, Y: leftEdge.Y})

	if err := s.start.run(context.Background()); !errors.Is(err, ErrCursorMoved) {
		t.Errorf("start = %v, want ErrCursorMoved", err)
	}
	expectEvents(t, s.screen.events())
}

func TestAMouseDownTheWindowDidNotGetIsReleasedWhereItWasPosted(t *testing.T) {
	s := startOnTheLeftEdge(appKitAnswers{window: windowReady, seesDown: false, begins: true})

	if err := s.start.run(context.Background()); !errors.Is(err, ErrDownNotSeen) {
		t.Errorf("start = %v, want ErrDownNotSeen", err)
	}
	expectEvents(t, s.screen.events(), postedDown(leftEdge), postedUp(leftEdge))
	if s.button.pending() {
		t.Error("the mouse down is still counted as held")
	}
}

func TestADragThatDoesNotBeginIsBroughtBackToTheWindowAndReleasedThere(t *testing.T) {
	s := startOnTheLeftEdge(appKitAnswers{window: windowReady, seesDown: true, begins: false})

	if err := s.start.run(context.Background()); !errors.Is(err, ErrDragDidNotBegin) {
		t.Errorf("start = %v, want ErrDragDidNotBegin", err)
	}
	want := append([]MouseEvent{postedDown(leftEdge)}, stepsRightOf(leftEdge)...)
	want = append(want, postedDrag(leftEdge, -18), postedUp(leftEdge))
	expectEvents(t, s.screen.events(), want...)
}

func TestACancelledStartReleasesTheMouseDownItPosted(t *testing.T) {
	s := startOnTheLeftEdge(appKitAnswers{window: windowReady, seesDown: true, begins: false})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	react := s.screen.onPost
	s.screen.onPost = func(mouse MouseEvent) {
		react(mouse)
		if mouse.Action == MouseDragged && mouse.At.X == leftEdge.X+6 {
			cancel()
		}
	}

	if err := s.start.run(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("start = %v, want context.Canceled", err)
	}
	events := s.screen.events()
	if !everyDownHasItsUp(events) {
		t.Errorf("a posted mouse down was left without its up: %+v", events)
	}
	if last := events[len(events)-1]; last != postedUp(leftEdge) {
		t.Errorf("the last event is %+v, want the mouse up back in the window", last)
	}
}

func TestAStartCancelledBeforehandPostsNothing(t *testing.T) {
	s := startOnTheLeftEdge(appKitAnswers{window: windowReady, seesDown: true, begins: true})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := s.start.run(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("start = %v, want context.Canceled", err)
	}
	expectEvents(t, s.screen.events())
}

func TestEveryFailedStartLeavesNoMouseDownWithoutItsUp(t *testing.T) {
	for _, answers := range []appKitAnswers{
		{window: windowNotShown},
		{window: windowCovered},
		{window: windowReady},
		{window: windowReady, seesDown: true},
		{window: windowReady, begins: true},
	} {
		s := startOnTheLeftEdge(answers)
		if err := s.start.run(context.Background()); err == nil {
			t.Errorf("%+v: the start did not fail", answers)
		}
		if events := s.screen.events(); !everyDownHasItsUp(events) || s.button.pending() {
			t.Errorf("%+v: a posted mouse down was left without its up: %+v", answers, events)
		}
	}
}

func TestAHeldDownIsReleasedOnceAndOnlyByWhoeverStillHoldsIt(t *testing.T) {
	screen := &dropScreen{cursor: leftEdge}
	button := newHeldDown(screen, leftEdge)
	if button.releaseAtHome() || button.releaseHere() {
		t.Error("a button never pressed was released")
	}

	button.press()
	button.handOver()
	if button.releaseAtHome() || button.releaseHere() {
		t.Error("a button handed over to the end of the session was released again")
	}
	expectEvents(t, screen.events(), postedDown(leftEdge))
}

func TestAHeldDownMovedByTheReplayIsDraggedBackToTheWindowBeforeItsUp(t *testing.T) {
	screen := &dropScreen{cursor: leftEdge}
	button := newHeldDown(screen, leftEdge)
	button.press()
	button.drag(Point{X: 3, Y: 400})
	finder := Point{X: 803, Y: 250}
	screen.moveTo(finder)

	if !button.releaseAtHome() {
		t.Fatal("the held button was not released")
	}
	if button.releaseAtHome() {
		t.Error("the held button was released twice")
	}
	expectEvents(t, screen.events(),
		postedDown(leftEdge),
		postedDrag(Point{X: 3, Y: 400}, 3),
		MouseEvent{Action: MouseDragged, At: leftEdge, Clicks: 1, DX: -803, DY: 150},
		postedUp(leftEdge),
	)
}

func TestAHeldDownLeftAfterTheSessionIsReleasedWhereTheCursorIs(t *testing.T) {
	screen := &dropScreen{cursor: leftEdge}
	button := newHeldDown(screen, leftEdge)
	button.press()
	screen.moveTo(Point{X: 803, Y: 250})

	if !button.releaseHere() {
		t.Fatal("the held button was not released")
	}
	expectEvents(t, screen.events(), postedDown(leftEdge), postedUp(Point{X: 803, Y: 250}))
}

type recordedWrite struct {
	file        int
	destination string
}

type writesUnderTest struct {
	mu      sync.Mutex
	written []recordedWrite
	fail    map[int]error
}

func (w *writesUnderTest) write(ctx context.Context, file int, destination string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.written = append(w.written, recordedWrite{file, destination})
	return w.fail[file]
}

func (w *writesUnderTest) calls() []recordedWrite {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.written)
}

func isClosed(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}

func TestEveryPromiseKeptClosesKept(t *testing.T) {
	writes := &writesUnderTest{}
	promised := newPromises(2)

	if err := promised.keep(context.Background(), 1, "/Users/me/Desktop/b.txt", writes.write); err != nil {
		t.Fatalf("keep 1: %v", err)
	}
	if isClosed(promised.kept) {
		t.Fatal("kept closed with one of two files written")
	}
	if err := promised.keep(context.Background(), 0, "/Users/me/Desktop/a.txt", writes.write); err != nil {
		t.Fatalf("keep 0: %v", err)
	}
	if !isClosed(promised.kept) {
		t.Error("kept stayed open with every file written")
	}
	want := []recordedWrite{{1, "/Users/me/Desktop/b.txt"}, {0, "/Users/me/Desktop/a.txt"}}
	if got := writes.calls(); !slices.Equal(got, want) {
		t.Errorf("writes = %+v, want %+v", got, want)
	}
}

func TestAPromiseThatFailedCanBeAskedAgainAndOneKeptCannot(t *testing.T) {
	failure := errors.New("the disk is full")
	writes := &writesUnderTest{fail: map[int]error{0: failure}}
	promised := newPromises(1)

	if err := promised.keep(context.Background(), 0, "/tmp/a.txt", writes.write); !errors.Is(err, failure) {
		t.Fatalf("keep = %v, want the failure of the write", err)
	}
	if isClosed(promised.kept) {
		t.Fatal("kept closed after a failed write")
	}
	writes.fail = nil
	if err := promised.keep(context.Background(), 0, "/tmp/a.txt", writes.write); err != nil {
		t.Fatalf("asked again: %v", err)
	}
	if err := promised.keep(context.Background(), 0, "/tmp/a.txt", writes.write); !errors.Is(err, ErrPromiseAskedTwice) {
		t.Errorf("a kept promise asked again gave %v, want ErrPromiseAskedTwice", err)
	}
	if got := len(writes.calls()); got != 2 {
		t.Errorf("the file was written %d times, want 2", got)
	}
}

func TestAPromiseBeingWrittenCannotBeAskedAgain(t *testing.T) {
	promised := newPromises(1)
	writing, finish := make(chan struct{}), make(chan struct{})
	slow := func(ctx context.Context, file int, destination string) error {
		close(writing)
		<-finish
		return nil
	}
	first := make(chan error)
	go func() { first <- promised.keep(context.Background(), 0, "/tmp/a.txt", slow) }()
	<-writing

	if err := promised.keep(context.Background(), 0, "/tmp/a.txt", slow); !errors.Is(err, ErrPromiseAskedTwice) {
		t.Errorf("asked during the write: %v, want ErrPromiseAskedTwice", err)
	}
	close(finish)
	if err := <-first; err != nil {
		t.Errorf("the first write: %v", err)
	}
}

func TestAPromiseIsNotWrittenForABadAskOrAfterTheDropClosed(t *testing.T) {
	writes := &writesUnderTest{}
	promised := newPromises(1)

	if err := promised.keep(context.Background(), 1, "/tmp/a.txt", writes.write); !errors.Is(err, ErrNoSuchPromise) {
		t.Errorf("file 1 of 1 gave %v, want ErrNoSuchPromise", err)
	}
	if err := promised.keep(context.Background(), -1, "/tmp/a.txt", writes.write); !errors.Is(err, ErrNoSuchPromise) {
		t.Errorf("file -1 gave %v, want ErrNoSuchPromise", err)
	}
	if err := promised.keep(context.Background(), 0, "Desktop/a.txt", writes.write); !errors.Is(err, ErrDestinationNotAbsolute) {
		t.Errorf("a relative destination gave %v, want ErrDestinationNotAbsolute", err)
	}
	promised.close()
	if err := promised.keep(context.Background(), 0, "/tmp/a.txt", writes.write); !errors.Is(err, ErrDropClosed) {
		t.Errorf("after the drop closed: %v, want ErrDropClosed", err)
	}
	if got := writes.calls(); len(got) != 0 {
		t.Errorf("writes = %+v, want none", got)
	}
}
