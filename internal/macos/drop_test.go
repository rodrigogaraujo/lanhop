package macos

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

type journal struct {
	mu      sync.Mutex
	entries []string
}

func (j *journal) note(format string, args ...any) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.entries = append(j.entries, fmt.Sprintf(format, args...))
}

func (j *journal) read() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return slices.Clone(j.entries)
}

var mouseActionNames = map[MouseAction]string{MouseMoved: "moved", MouseDragged: "dragged", MouseDown: "down", MouseUp: "up"}

type dropScreen struct {
	mu      sync.Mutex
	cursor  Point
	posted  []MouseEvent
	keys    []KeyPress
	onPost  func(MouseEvent)
	onKey   func(KeyPress)
	journal *journal
}

func (s *dropScreen) PostMouse(mouse MouseEvent) {
	s.mu.Lock()
	s.posted = append(s.posted, mouse)
	s.cursor = mouse.At
	react := s.onPost
	s.mu.Unlock()
	s.journal.note("%s %v,%v", mouseActionNames[mouse.Action], mouse.At.X, mouse.At.Y)
	if react != nil {
		react(mouse)
	}
}

func (s *dropScreen) PostKey(key KeyPress) {
	s.mu.Lock()
	s.keys = append(s.keys, key)
	react := s.onKey
	s.mu.Unlock()
	s.journal.note("key %#x down %v", key.Code, key.Down)
	if react != nil {
		react(key)
	}
}

func (s *dropScreen) keysPosted() []KeyPress {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.keys)
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
		{windowMissesTheCursor, ErrDropWindowMissed},
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
		{window: windowMissesTheCursor},
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

func TestTheStartWaitsThroughAWindowCoveredForAMoment(t *testing.T) {
	s := startOnTheLeftEdge(appKitAnswers{window: windowReady, seesDown: true, begins: true})
	var mu sync.Mutex
	states := []windowState{windowCovered, windowCovered, windowMissesTheCursor, windowReady}
	s.start.window = func() windowState {
		mu.Lock()
		defer mu.Unlock()
		state := states[0]
		if len(states) > 1 {
			states = states[1:]
		}
		return state
	}

	if err := s.start.run(context.Background()); err != nil {
		t.Fatalf("a window covered for a moment, then ready, gave %v", err)
	}
	expectEvents(t, s.screen.events(), append([]MouseEvent{postedDown(leftEdge)}, stepsRightOf(leftEdge)...)...)
}

type fakeDropWindow struct {
	mu         sync.Mutex
	answers    []bool
	refuseLate bool
	takeLate   bool
	closeLate  bool
	journal    *journal
	onTake     func(call int)
	onClose    func()
	refused    bool
	movedTo    []Point
	closed     int
}

func (w *fakeDropWindow) state() windowState {
	return windowReady
}

func (w *fakeDropWindow) refuseDrops() error {
	w.journal.note("refuse")
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.refuseLate {
		return ErrMainThreadLate
	}
	w.refused = true
	return nil
}

func (w *fakeDropWindow) takeTheMouseAt(at Point) (bool, error) {
	w.journal.note("move %v,%v", at.X, at.Y)
	w.mu.Lock()
	w.movedTo = append(w.movedTo, at)
	call := len(w.movedTo)
	taken := len(w.answers) > 0 && w.answers[0]
	if len(w.answers) > 1 {
		w.answers = w.answers[1:]
	}
	late, react := w.takeLate, w.onTake
	w.mu.Unlock()
	if react != nil {
		react(call)
	}
	if late {
		return false, ErrMainThreadLate
	}
	return taken, nil
}

func (w *fakeDropWindow) close() error {
	w.mu.Lock()
	w.closed++
	late, react := w.closeLate, w.onClose
	w.mu.Unlock()
	if late {
		return ErrMainThreadLate
	}
	if react != nil {
		react()
	}
	return nil
}

func (w *fakeDropWindow) seen() (refused bool, movedTo []Point, closed int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.refused, slices.Clone(w.movedTo), w.closed
}

const shortReleaseLimit = 50 * time.Millisecond

var (
	finderUnderTheCursor = Point{X: 803, Y: 250}
	mailUnderTheCursor   = Point{X: 900, Y: 300}
	nudgedFurther        = Point{X: 910, Y: 305}
)

type endUnderTest struct {
	screen  *dropScreen
	window  *fakeDropWindow
	button  *heldDown
	ended   *latch
	journal *journal
	mu      sync.Mutex
	logged  []string
	end     sessionEnd
}

func (e *endUnderTest) logf(format string, args ...any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.logged = append(e.logged, fmt.Sprintf(format, args...))
}

func (e *endUnderTest) logLines() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.logged)
}

func runningSessionOver(window *fakeDropWindow) *endUnderTest {
	record := &journal{}
	screen := &dropScreen{cursor: leftEdge}
	e := &endUnderTest{screen: screen, window: window, button: newHeldDown(screen, leftEdge), ended: newLatch(), journal: record}
	e.button.press()
	e.button.drag(Point{X: 3, Y: 400})
	screen.moveTo(finderUnderTheCursor)
	screen.journal = record
	window.journal = record
	screen.onPost = func(mouse MouseEvent) {
		if mouse.Action == MouseUp {
			e.ended.fire()
		}
	}
	e.end = sessionEnd{
		button:       e.button,
		window:       window,
		ended:        e.ended.done,
		downAtEnd:    func() bool { return false },
		releaseLimit: time.Second,
		releasePoll:  5 * time.Millisecond,
		endLimit:     time.Second,
		logf:         e.logf,
	}
	return e
}

func spot(p Point) string {
	return fmt.Sprintf("%v,%v", p.X, p.Y)
}

func escaped() []string {
	return []string{fmt.Sprintf("key %#x down true", macOSEscape), fmt.Sprintf("key %#x down false", macOSEscape)}
}

func movesUntilTheEnd(entries []string) int {
	count := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry, "move ") {
			count++
		}
	}
	return count
}

func TestAClosedSessionRefusesDropsThenReleasesOverTheWindowAfterADrag(t *testing.T) {
	e := runningSessionOver(&fakeDropWindow{answers: []bool{true}})

	e.end.run()

	want := []string{"refuse", "move " + spot(finderUnderTheCursor), "dragged " + spot(finderUnderTheCursor), "up " + spot(finderUnderTheCursor)}
	if got := e.journal.read(); !slices.Equal(got, want) {
		t.Errorf("Close did %q, want %q", got, want)
	}
	if got := e.screen.keysPosted(); len(got) != 0 {
		t.Errorf("keys %+v were posted when lanhop's window took the mouse", got)
	}
	if got := e.logLines(); len(got) != 0 {
		t.Errorf("logged %q when lanhop's window took the mouse", got)
	}
}

func TestAClosedSessionReleasesOnlyOnceTheWindowTakesTheMouse(t *testing.T) {
	e := runningSessionOver(&fakeDropWindow{answers: []bool{false, false, true}})

	e.end.run()

	under := spot(finderUnderTheCursor)
	want := []string{"refuse", "move " + under, "move " + under, "move " + under, "dragged " + under, "up " + under}
	if got := e.journal.read(); !slices.Equal(got, want) {
		t.Errorf("Close did %q, want %q", got, want)
	}
}

func TestAClosedSessionWhoseWindowNeverTakesTheMouseEscapesBeforeTheRelease(t *testing.T) {
	e := runningSessionOver(&fakeDropWindow{answers: []bool{false}})
	e.end.releaseLimit = shortReleaseLimit
	var escapedAt, draggedAt time.Time
	e.screen.onKey = func(key KeyPress) {
		if !key.Down {
			escapedAt = time.Now()
		}
	}
	react := e.screen.onPost
	e.screen.onPost = func(mouse MouseEvent) {
		if mouse.Action == MouseDragged {
			draggedAt = time.Now()
		}
		react(mouse)
	}

	e.end.run()

	if gap := draggedAt.Sub(escapedAt); gap < escapeBeforeRelease {
		t.Errorf("the dragged event followed Escape after %v, want at least %v", gap, escapeBeforeRelease)
	}

	got := e.journal.read()
	if len(got) < 6 || got[0] != "refuse" || movesUntilTheEnd(got) < 2 {
		t.Fatalf("Close did %q, want a refusal, then moves until the limit", got)
	}
	under := spot(finderUnderTheCursor)
	if tail, want := got[len(got)-4:], append(escaped(), "dragged "+under, "up "+under); !slices.Equal(tail, want) {
		t.Errorf("Close ended with %q, want %q", tail, want)
	}
	if lines := e.logLines(); len(lines) != 1 || !strings.Contains(lines[0], "another window takes the mouse there") || !strings.Contains(lines[0], "Escape was posted") || !strings.HasSuffix(lines[0], "with drops refused") {
		t.Errorf("logged %q", lines)
	}
}

func TestARefusalTheMainThreadAnsweredLateIsSaidAndTheReleaseStillGoesOverTheWindow(t *testing.T) {
	e := runningSessionOver(&fakeDropWindow{answers: []bool{true}, refuseLate: true})

	e.end.run()

	under := spot(finderUnderTheCursor)
	if got, want := e.journal.read(), []string{"refuse", "move " + under, "dragged " + under, "up " + under}; !slices.Equal(got, want) {
		t.Errorf("Close did %q, want %q", got, want)
	}
	if lines := e.logLines(); len(lines) != 1 || !strings.Contains(lines[0], "not told to refuse drops") || !strings.Contains(lines[0], ErrMainThreadLate.Error()) {
		t.Errorf("logged %q", lines)
	}
}

func TestAMoveTheMainThreadAnsweredLateEndsInEscapeAndSaysDropsWereNotRefused(t *testing.T) {
	e := runningSessionOver(&fakeDropWindow{answers: []bool{true}, refuseLate: true, takeLate: true})
	e.end.releaseLimit = shortReleaseLimit

	e.end.run()

	got := e.journal.read()
	under := spot(finderUnderTheCursor)
	if tail, want := got[len(got)-4:], append(escaped(), "dragged "+under, "up "+under); !slices.Equal(tail, want) {
		t.Errorf("Close ended with %q, want %q", tail, want)
	}
	lines := e.logLines()
	if len(lines) != 2 || !strings.Contains(lines[1], ErrMainThreadLate.Error()) || !strings.HasSuffix(lines[1], "without drops refused") {
		t.Errorf("logged %q", lines)
	}
}

func TestAClosedSessionFollowsTheCursorAndReleasesWhereTheWindowTookTheMouse(t *testing.T) {
	window := &fakeDropWindow{answers: []bool{false, true}}
	e := runningSessionOver(window)
	window.onTake = func(call int) {
		if call == 1 {
			e.screen.moveTo(mailUnderTheCursor)
		} else {
			e.screen.moveTo(nudgedFurther)
		}
	}

	e.end.run()

	want := []string{"refuse", "move " + spot(finderUnderTheCursor), "move " + spot(mailUnderTheCursor), "dragged " + spot(mailUnderTheCursor), "up " + spot(mailUnderTheCursor)}
	if got := e.journal.read(); !slices.Equal(got, want) {
		t.Errorf("Close did %q, want %q", got, want)
	}
}

func TestAClosedSessionThatEndsWhileTheWindowMovesPostsNothingMore(t *testing.T) {
	window := &fakeDropWindow{answers: []bool{false}}
	e := runningSessionOver(window)
	window.onTake = func(int) { e.ended.fire() }

	e.end.run()

	if got, want := e.journal.read(), []string{"refuse", "move " + spot(finderUnderTheCursor)}; !slices.Equal(got, want) {
		t.Errorf("Close did %q after the session ended, want %q", got, want)
	}
}

func TestAClosedSessionThatAlreadyEndedWithTheButtonDownIsReleasedWhereTheCursorIs(t *testing.T) {
	e := runningSessionOver(&fakeDropWindow{answers: []bool{true}})
	e.ended.fire()
	e.end.downAtEnd = func() bool { return true }

	e.end.run()

	if got, want := e.journal.read(), []string{"up " + spot(finderUnderTheCursor)}; !slices.Equal(got, want) {
		t.Errorf("Close did %q, want %q", got, want)
	}
}
