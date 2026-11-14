package macos

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type PromisedFile struct {
	Name   string
	Folder bool
}

type WriteFile func(ctx context.Context, file int, destination string) error

type Operation uint64

const (
	OperationNone    Operation = 0
	OperationCopy    Operation = 1
	OperationLink    Operation = 2
	OperationGeneric Operation = 4
	OperationPrivate Operation = 8
	OperationMove    Operation = 16
	OperationDelete  Operation = 32
)

var operationNames = []struct {
	operation Operation
	name      string
}{
	{OperationCopy, "copy"},
	{OperationLink, "link"},
	{OperationGeneric, "generic"},
	{OperationPrivate, "private"},
	{OperationMove, "move"},
	{OperationDelete, "delete"},
}

func (o Operation) Accepted() bool {
	return o&OperationCopy != 0
}

func (o Operation) String() string {
	if o == OperationNone {
		return "none"
	}
	var names []string
	unknown := o
	for _, known := range operationNames {
		if o&known.operation != 0 {
			names = append(names, known.name)
			unknown &^= known.operation
		}
	}
	if unknown != 0 {
		names = append(names, fmt.Sprintf("0x%x", uint64(unknown)))
	}
	return strings.Join(names, "+")
}

const (
	longestPromisedName = 255
	folderKind          = "public.folder"
)

var (
	ErrNoPromisedFiles      = errors.New("macos: a drop needs at least one file")
	ErrTooManyPromisedFiles = errors.New("macos: a drop holds more files and folders than lanhop drops at once")
	ErrPromisedName         = errors.New("macos: a dropped file needs a plain name that macOS can create")
	ErrNoWriter             = errors.New("macos: a drop needs a way to write its files")
)

func checkPromisedFiles(files []PromisedFile, most int) error {
	if len(files) == 0 {
		return ErrNoPromisedFiles
	}
	if len(files) > most {
		return fmt.Errorf("%w: %d, at most %d", ErrTooManyPromisedFiles, len(files), most)
	}
	for _, file := range files {
		if !plainName(file.Name) {
			return fmt.Errorf("%w: %q", ErrPromisedName, file.Name)
		}
	}
	return nil
}

func plainName(name string) bool {
	return name != "" && name != "." && name != ".." && len(name) <= longestPromisedName &&
		utf8.ValidString(name) && !strings.ContainsAny(name, "/\x00")
}

func promisedNames(files []PromisedFile) []string {
	names := make([]string, len(files))
	for i, file := range files {
		names[i] = file.Name
	}
	return names
}

func promisedKinds(files []PromisedFile) []string {
	kinds := make([]string, len(files))
	for i, file := range files {
		if file.Folder {
			kinds[i] = folderKind
		}
	}
	return kinds
}

const (
	dropWindowSide  = 64.0
	dragStepPoints  = 3.0
	dragSteps       = 6
	cursorTolerance = 2.0
)

type dragPlan struct {
	start Point
	steps []Point
}

func planDrag(cursor Point, displays []Rect) dragPlan {
	direction := 1.0
	for _, display := range displays {
		if display.contains(cursor) && cursor.X >= display.X+display.Width/2 {
			direction = -1
		}
	}
	steps := make([]Point, dragSteps)
	for i := range steps {
		steps[i] = Point{X: cursor.X + direction*dragStepPoints*float64(i+1), Y: cursor.Y}
	}
	return dragPlan{start: cursor, steps: steps}
}

type mousePoster interface {
	PostMouse(MouseEvent)
	Cursor() Point
}

type heldDown struct {
	mu     sync.Mutex
	screen mousePoster
	home   Point
	at     Point
	down   bool
	moved  bool
}

func newHeldDown(screen mousePoster, home Point) *heldDown {
	return &heldDown{screen: screen, home: home, at: home}
}

func (h *heldDown) press() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.down = true
	h.screen.PostMouse(MouseEvent{Action: MouseDown, At: h.home, Clicks: 1})
}

func (h *heldDown) drag(to Point) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.down {
		return
	}
	h.postDrag(h.at, to)
	h.moved = true
}

func (h *heldDown) postDrag(from, to Point) {
	h.screen.PostMouse(MouseEvent{
		Action: MouseDragged,
		At:     to,
		Clicks: 1,
		DX:     int32(math.Round(to.X - from.X)),
		DY:     int32(math.Round(to.Y - from.Y)),
	})
	h.at = to
}

func (h *heldDown) releaseAtHome() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.down {
		return false
	}
	if h.moved {
		h.postDrag(h.screen.Cursor(), h.home)
	}
	h.screen.PostMouse(MouseEvent{Action: MouseUp, At: h.home, Clicks: 1})
	h.down = false
	return true
}

func (h *heldDown) releaseHere() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.down {
		return false
	}
	h.screen.PostMouse(MouseEvent{Action: MouseUp, At: h.screen.Cursor(), Clicks: 1})
	h.down = false
	return true
}

func (h *heldDown) handOver() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.down = false
}

func (h *heldDown) pending() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.down
}

type windowState int

const (
	windowReady windowState = iota
	windowNotShown
	windowCovered
	windowMissesTheCursor
)

type dropWindow interface {
	state() windowState
	takesTheMouseAtHome() bool
	close() (sessionPending bool)
}

var (
	ErrDropWindowNotShown = errors.New("macos: the window of lanhop did not appear under the cursor, so nothing was posted")
	ErrDropWindowCovered  = errors.New("macos: another window covers the cursor, so nothing was posted")
	ErrDropWindowMissed   = errors.New("macos: the window of lanhop does not take the mouse at the cursor, so nothing was posted")
	ErrCursorMoved        = errors.New("macos: the cursor moved before the mouse down that starts the drag, so nothing was posted")
	ErrDownNotSeen        = errors.New("macos: the window of lanhop did not get the posted mouse down, so it was released")
	ErrDragDidNotBegin    = errors.New("macos: no drag session began after the posted mouse down, so it was released")
)

type dragTiming struct {
	showLimit  time.Duration
	showPoll   time.Duration
	downLimit  time.Duration
	firstStep  time.Duration
	nextStep   time.Duration
	beginLimit time.Duration
}

var timingOfTheProbe = dragTiming{
	showLimit:  time.Second,
	showPoll:   10 * time.Millisecond,
	downLimit:  time.Second,
	firstStep:  100 * time.Millisecond,
	nextStep:   20 * time.Millisecond,
	beginLimit: 2 * time.Second,
}

type dragStart struct {
	plan     dragPlan
	button   *heldDown
	window   func() windowState
	downSeen <-chan struct{}
	began    <-chan struct{}
	timing   dragTiming
}

func (s dragStart) run(ctx context.Context) error {
	if err := s.waitForTheWindow(ctx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !within(s.button.screen.Cursor(), s.plan.start, cursorTolerance) {
		return ErrCursorMoved
	}
	s.button.press()
	if err := waitFor(ctx, s.downSeen, s.timing.downLimit, ErrDownNotSeen); err != nil {
		s.button.releaseAtHome()
		return err
	}
	pause := s.timing.firstStep
	for _, step := range s.plan.steps {
		if err := sleepUnlessDone(ctx, pause); err != nil {
			s.button.releaseAtHome()
			return err
		}
		s.button.drag(step)
		pause = s.timing.nextStep
	}
	if err := waitFor(ctx, s.began, s.timing.beginLimit, ErrDragDidNotBegin); err != nil {
		s.button.releaseAtHome()
		return err
	}
	return nil
}

func (s dragStart) waitForTheWindow(ctx context.Context) error {
	deadline := time.Now().Add(s.timing.showLimit)
	for {
		state := s.window()
		switch state {
		case windowReady:
			return nil
		case windowCovered:
			return ErrDropWindowCovered
		}
		if !time.Now().Before(deadline) {
			if state == windowMissesTheCursor {
				return ErrDropWindowMissed
			}
			return ErrDropWindowNotShown
		}
		if err := sleepUnlessDone(ctx, s.timing.showPoll); err != nil {
			return err
		}
	}
}

func within(a, b Point, tolerance float64) bool {
	return abs(a.X-b.X) <= tolerance && abs(a.Y-b.Y) <= tolerance
}

func waitFor(ctx context.Context, signal <-chan struct{}, limit time.Duration, late error) error {
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case <-signal:
		return nil
	case <-timer.C:
		return late
	case <-ctx.Done():
		return ctx.Err()
	}
}

func sleepUnlessDone(ctx context.Context, pause time.Duration) error {
	timer := time.NewTimer(pause)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type latch struct {
	closing sync.Once
	done    chan struct{}
}

func newLatch() *latch {
	return &latch{done: make(chan struct{})}
}

func (l *latch) fire() {
	l.closing.Do(func() { close(l.done) })
}

type promiseState int

const (
	promiseWaiting promiseState = iota
	promiseWriting
	promiseKept
	promiseBroken
)

var (
	ErrDropClosed             = errors.New("macos: lanhop no longer offers the files of this drop")
	ErrNoSuchPromise          = errors.New("macos: the destination asked for a file this drop does not offer")
	ErrPromiseAskedTwice      = errors.New("macos: the destination asked twice for the same file")
	ErrDestinationNotAbsolute = errors.New("macos: the destination of a dropped file is not an absolute path")
)

type promises struct {
	mu      sync.Mutex
	states  []promiseState
	waiting int
	closed  bool
	kept    chan struct{}
}

func newPromises(count int) *promises {
	return &promises{states: make([]promiseState, count), waiting: count, kept: make(chan struct{})}
}

func (p *promises) keep(ctx context.Context, file int, destination string, write WriteFile) error {
	if !filepath.IsAbs(destination) {
		return fmt.Errorf("%w: %q", ErrDestinationNotAbsolute, destination)
	}
	if err := p.ask(file); err != nil {
		return err
	}
	err := write(ctx, file, destination)
	p.settle(file, err == nil)
	return err
}

func (p *promises) ask(file int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case p.closed:
		return ErrDropClosed
	case file < 0 || file >= len(p.states):
		return fmt.Errorf("%w: %d", ErrNoSuchPromise, file)
	case p.states[file] == promiseWriting || p.states[file] == promiseKept:
		return fmt.Errorf("%w: %d", ErrPromiseAskedTwice, file)
	}
	p.states[file] = promiseWriting
	return nil
}

func (p *promises) settle(file int, written bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !written {
		p.states[file] = promiseBroken
		return
	}
	p.states[file] = promiseKept
	p.waiting--
	if p.waiting == 0 {
		close(p.kept)
	}
}

func (p *promises) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
}
