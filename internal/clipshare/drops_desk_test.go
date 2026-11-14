package clipshare

import (
	"context"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/edge"
	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/features"
	"github.com/rodrigogaraujo/lanhop/internal/identity"
	"github.com/rodrigogaraujo/lanhop/internal/sharer"
	"github.com/rodrigogaraujo/lanhop/internal/transport"
)

const textFilesAndDrags = textAndFiles | 1<<features.Drag

var onTheCentral *sideUnderTest

type fakeDrops struct {
	mutex       sync.Mutex
	sessions    []*fakeDropSession
	arrivedHeld <-chan struct{}
}

func (f *fakeDrops) StartDrop(ctx context.Context, files []DroppedFile) (DropSession, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	session := &fakeDropSession{
		files:       slices.Clone(files),
		taken:       make(chan struct{}),
		ended:       make(chan struct{}),
		cancelled:   make(chan struct{}),
		arrivedHeld: f.arrivedHeld,
	}
	f.sessions = append(f.sessions, session)
	return session, nil
}

func (f *fakeDrops) holdArrivedUntil(released <-chan struct{}) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.arrivedHeld = released
}

func (f *fakeDrops) started() []*fakeDropSession {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return slices.Clone(f.sessions)
}

type fakeDropSession struct {
	files       []DroppedFile
	taken       chan struct{}
	ended       chan struct{}
	cancelled   chan struct{}
	taking      sync.Once
	ending      sync.Once
	cancelling  sync.Once
	arrivedHeld <-chan struct{}

	mutex                     sync.Mutex
	handed                    [][]string
	calls                     []string
	goneBeforeArrivedReturned []string
}

func (s *fakeDropSession) Arrived(paths []string) {
	s.mutex.Lock()
	s.handed = append(s.handed, slices.Clone(paths))
	s.mutex.Unlock()
	if s.arrivedHeld != nil {
		<-s.arrivedHeld
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for _, path := range paths {
		if _, err := os.Lstat(path); err != nil {
			s.goneBeforeArrivedReturned = append(s.goneBeforeArrivedReturned, path)
		}
	}
	s.calls = append(s.calls, "Arrived")
}

func (s *fakeDropSession) Cancel() {
	s.mutex.Lock()
	s.calls = append(s.calls, "Cancel")
	s.mutex.Unlock()
	s.cancelling.Do(func() { close(s.cancelled) })
}

func (s *fakeDropSession) Taken() <-chan struct{} {
	return s.taken
}

func (s *fakeDropSession) EndedWithoutDrop() <-chan struct{} {
	return s.ended
}

func (s *fakeDropSession) take() {
	s.taking.Do(func() { close(s.taken) })
}

func (s *fakeDropSession) endWithoutDrop() {
	s.ending.Do(func() { close(s.ended) })
}

func (s *fakeDropSession) wasCancelled() bool {
	select {
	case <-s.cancelled:
		return true
	default:
		return false
	}
}

func (s *fakeDropSession) handedPaths() [][]string {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return slices.Clone(s.handed)
}

func (s *fakeDropSession) callsInOrder() []string {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return slices.Clone(s.calls)
}

func (s *fakeDropSession) pathsGoneBeforeArrivedReturned() []string {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return slices.Clone(s.goneBeforeArrivedReturned)
}

func heldUntil(released <-chan struct{}, await AwaitData) AwaitData {
	if released == nil {
		return await
	}
	return func(ctx context.Context, side identity.Fingerprint, announce func(transfer int32) error) (*transport.DataConn, error) {
		select {
		case <-released:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return await(ctx, side, announce)
	}
}

func newDropDesk(t *testing.T, sideNames ...string) *deskUnderTest {
	t.Helper()
	board := holding("what the central held before")
	return newDeskWith(t, deskSetup{board: board, open: opening(takingFiles{board}), files: true, drops: true}, sideNames...)
}

func newRelayingDesk(t *testing.T, sideNames ...string) *deskUnderTest {
	t.Helper()
	board := holding("what the central held before")
	return newDeskWith(t, deskSetup{board: board, open: opening(takingFiles{board}), files: true, drops: true, relaysOnly: true}, sideNames...)
}

func newSlowDropDesk(t *testing.T, bytesPerSecond int, sideNames ...string) *deskUnderTest {
	t.Helper()
	board := holding("what the central held before")
	return newDeskWith(t, deskSetup{board: board, open: opening(takingFiles{board}), files: true, drops: true, bytesPerSecond: bytesPerSecond}, sideNames...)
}

func askCentral[T any](t *testing.T, c *Central, question func() T) T {
	t.Helper()
	answer := make(chan T, 1)
	c.tell(func() { answer <- question() })
	select {
	case got := <-answer:
		return got
	case <-time.After(testPatience):
		t.Fatal("the central runtime did not answer")
		var nothing T
		return nothing
	}
}

func askSide[T any](t *testing.T, s *Side, question func() T) T {
	t.Helper()
	answer := make(chan T, 1)
	s.inbox.put(func() { answer <- question() })
	select {
	case got := <-answer:
		return got
	case <-time.After(testPatience):
		t.Fatal("the side machine runtime did not answer")
		var nothing T
		return nothing
	}
}

func (d *deskUnderTest) machineNamed(name string) *sideMachine {
	for l, machine := range d.runtime.sides {
		if l.name == name {
			return machine
		}
	}
	return nil
}

func (d *deskUnderTest) waitUntilTakingDrops(sides ...*sideUnderTest) {
	d.t.Helper()
	d.waitUntilSharingFiles(sides...)
	for _, side := range sides {
		eventually(d.t, "the central seeing "+side.name+" take drops", func() bool {
			return askCentral(d.t, d.runtime, func() bool {
				machine := d.machineNamed(side.name)
				return machine != nil && machine.drops
			})
		})
		eventually(d.t, side.name+" seeing the central take drops", func() bool {
			return askSide(d.t, side.runtime, func() bool { return side.runtime.centralDrags })
		})
	}
}

func nameOf(side *sideUnderTest) string {
	if side == onTheCentral {
		return "desk"
	}
	return side.name
}

func (d *deskUnderTest) crosses(origin, to *sideUnderTest) error {
	d.t.Helper()
	return d.runtime.DragCrossed(nameOf(origin), nameOf(to))
}

func (d *deskUnderTest) seesADragOn(side *sideUnderTest) bool {
	d.t.Helper()
	return askCentral(d.t, d.runtime, func() bool {
		if side == onTheCentral {
			return d.runtime.dragging
		}
		machine := d.machineNamed(side.name)
		return machine != nil && machine.dragging
	})
}

func (d *deskUnderTest) sideDrags(side *sideUnderTest, paths ...string) {
	d.t.Helper()
	side.runtime.DragSeen(paths, edge.Bottom)
	eventually(d.t, "the central seeing the drag on "+side.name, func() bool { return d.seesADragOn(side) })
}

func (d *deskUnderTest) centralDrags(paths ...string) {
	d.t.Helper()
	d.runtime.DragSeen(paths)
	eventually(d.t, "the central seeing its own drag", func() bool { return d.seesADragOn(onTheCentral) })
}

func (d *deskUnderTest) waitUntilFocusIsOnTheCentral() {
	d.t.Helper()
	eventually(d.t, "the central taking focus back", func() bool {
		return askCentral(d.t, d.runtime, func() bool { return d.runtime.focusHere })
	})
}

func (d *deskUnderTest) focusMovesTo(side *sideUnderTest) {
	d.t.Helper()
	if side == onTheCentral {
		d.focusOn(thisMachine)
		d.waitUntilFocusIsOnTheCentral()
		return
	}
	d.focusOn(side.number)
	d.waitUntilTheCentralHasMovedFocusTo(side)
}

func (d *deskUnderTest) dragCrosses(from, to *sideUnderTest, paths ...string) {
	d.t.Helper()
	d.focusMovesTo(from)
	if from == onTheCentral {
		d.centralDrags(paths...)
	} else {
		d.sideDrags(from, paths...)
	}
	d.focusMovesTo(to)
	if err := d.crosses(from, to); err != nil {
		d.t.Fatalf("DragCrossed: %v", err)
	}
}

func (d *deskUnderTest) expectNoDropsLeft(sides ...*sideUnderTest) {
	d.t.Helper()
	eventually(d.t, "the central forgetting every drop", func() bool {
		return askCentral(d.t, d.runtime, func() int { return len(d.runtime.drops) }) == 0
	})
	for _, side := range sides {
		eventually(d.t, side.name+" forgetting every drop", func() bool {
			return askSide(d.t, side.runtime, func() int { return len(side.runtime.drops) }) == 0
		})
	}
}

func dropsOn(side *sideUnderTest, d *deskUnderTest) *fakeDrops {
	if side == onTheCentral {
		return d.drops
	}
	return side.drops
}

func waitForADrop(t *testing.T, name string, drops *fakeDrops) *fakeDropSession {
	t.Helper()
	eventually(t, "a drop starting on "+name, func() bool { return len(drops.started()) > 0 })
	started := drops.started()
	if len(started) != 1 {
		t.Fatalf("%d drops started on %s, want one", len(started), name)
	}
	return started[0]
}

func waitUntilHanded(t *testing.T, name string, session *fakeDropSession) []string {
	t.Helper()
	eventually(t, "the files reaching the drop on "+name, func() bool { return len(session.handedPaths()) > 0 })
	handed := session.handedPaths()
	if len(handed) != 1 {
		t.Fatalf("the drop on %s got files %d times, want once: %q", name, len(handed), handed)
	}
	return handed[0]
}

func waitUntilCancelled(t *testing.T, name string, session *fakeDropSession) {
	t.Helper()
	eventually(t, "the drop on "+name+" being cancelled", session.wasCancelled)
}

func expectNoDropStarted(t *testing.T, name string, drops *fakeDrops) {
	t.Helper()
	if started := drops.started(); len(started) > 0 {
		t.Errorf("%d drops started on %s, want none", len(started), name)
	}
}

func handledEverythingQueued(t *testing.T, s *Side) {
	t.Helper()
	askSide(t, s, func() bool { return true })
}

type frameRecorder struct {
	mutex  sync.Mutex
	frames []event.Event
}

func (r *frameRecorder) record(conn interface {
	Receive() (event.Event, error)
}) {
	for {
		frame, err := conn.Receive()
		if err != nil {
			return
		}
		r.mutex.Lock()
		r.frames = append(r.frames, frame)
		r.mutex.Unlock()
	}
}

func (r *frameRecorder) withCode(code uint16) []event.Event {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	var found []event.Event
	for _, frame := range r.frames {
		if frame.IsControl() && frame.Code == code {
			found = append(found, frame)
		}
	}
	return found
}

func (r *frameRecorder) all() []event.Event {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return slices.Clone(r.frames)
}

type handledFrames struct {
	mutex sync.Mutex
	from  map[string][]event.Event
}

func (h *handledFrames) of(conn *transport.Conn) sharer.Conn {
	return &handlingConn{Conn: conn, frames: h}
}

func (h *handledFrames) handled(name string, frame event.Event) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	if h.from == nil {
		h.from = make(map[string][]event.Event)
	}
	h.from[name] = append(h.from[name], frame)
}

func (h *handledFrames) dragEndsFrom(name string) int {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	ends := 0
	for _, frame := range h.from[name] {
		if files, _ := event.SplitDragFiles(frame.Value); frame.IsControl() && frame.Code == event.ControlDragFiles && files == 0 {
			ends++
		}
	}
	return ends
}

type handlingConn struct {
	*transport.Conn
	frames *handledFrames
	inHand *event.Event
}

func (c *handlingConn) Receive() (event.Event, error) {
	if c.inHand != nil {
		c.frames.handled(c.Peer().Name, *c.inHand)
		c.inHand = nil
	}
	frame, err := c.Conn.Receive()
	if err == nil {
		c.inHand = &frame
	}
	return frame, err
}

type describedDrags struct {
	mutex  sync.Mutex
	starts []SideDrag
	ends   []string
}

func (r *describedDrags) started(drag SideDrag) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.starts = append(r.starts, drag)
}

func (r *describedDrags) ended(origin string) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.ends = append(r.ends, origin)
}

func (r *describedDrags) seen() (starts []SideDrag, ends []string) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return slices.Clone(r.starts), slices.Clone(r.ends)
}
