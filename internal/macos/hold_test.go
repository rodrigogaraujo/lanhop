package macos

import (
	"sync"
	"testing"
)

var (
	whereTheCursorWas = Point{X: 700.5, Y: 300.25}
	movements         = []uint32{TypeMouseMoved, TypeLeftMouseDragged, TypeRightMouseDragged, TypeOtherMouseDragged}
)

type fakePointer struct {
	mutex    sync.Mutex
	cursor   Point
	displays []Rect
	putBack  int
}

func (p *fakePointer) Cursor() Point {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.cursor
}

func (p *fakePointer) Displays() []Rect {
	return p.displays
}

func (p *fakePointer) PlaceCursor(at Point) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.cursor = at
}

func (p *fakePointer) PutCursorBack(at Point) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.cursor = at
	p.putBack++
}

func (p *fakePointer) timesPutBack() int {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.putBack
}

func (p *fakePointer) moveBy(dx, dy int64) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.cursor.X += float64(dx)
	p.cursor.Y += float64(dy)
}

type macWithAMouse struct {
	pointer *fakePointer
	hold    *hold
}

func everything(Observation) bool {
	return true
}

func macWithTheCursorAt(at Point, accepts func(Observation) bool) *macWithAMouse {
	pointer := &fakePointer{cursor: at, displays: []Rect{laptopPanel}}
	return &macWithAMouse{pointer: pointer, hold: &hold{holdingScreen: pointer, accepts: accepts}}
}

func (m *macWithAMouse) mouseMoves(kind uint32, dx, dy int64) (kept bool) {
	m.pointer.moveBy(dx, dy)
	return m.hold.keeps(Observation{Type: kind, DX: dx, DY: dy})
}

func TestMovementKeptWhileTheInputIsTakenLeavesTheCursorWhereItWas(t *testing.T) {
	for _, kind := range movements {
		mac := macWithTheCursorAt(whereTheCursorWas, everything)
		mac.hold.take()

		keptFirst := mac.mouseMoves(kind, 40, 25)
		keptSecond := mac.mouseMoves(kind, -3, 90)

		if !keptFirst || !keptSecond {
			t.Errorf("movement of type %d passed on to this Mac while the input was taken", kind)
		}
		if at := mac.pointer.Cursor(); at != whereTheCursorWas {
			t.Errorf("after movement of type %d the cursor is at %+v, want it to stay at %+v", kind, at, whereTheCursorWas)
		}
	}
}

func TestKeysButtonsAndScrollingAreKeptWithoutPuttingTheCursorAnywhere(t *testing.T) {
	for _, kind := range []uint32{
		TypeKeyDown, TypeKeyUp, TypeFlagsChanged, TypeScrollWheel,
		TypeLeftMouseDown, TypeLeftMouseUp, TypeRightMouseDown, TypeRightMouseUp, TypeOtherMouseDown, TypeOtherMouseUp,
	} {
		mac := macWithTheCursorAt(whereTheCursorWas, everything)
		mac.hold.take()

		kept := mac.hold.keeps(Observation{Type: kind})

		if !kept {
			t.Errorf("input of type %d passed on to this Mac while the input was taken", kind)
		}
		if times := mac.pointer.timesPutBack(); times != 0 {
			t.Errorf("input of type %d put the cursor back %d times, want only movement to do that", kind, times)
		}
	}
}

func TestCursorFollowsTheMouseBeforeTheInputIsTakenAndAfterItIsGivenBack(t *testing.T) {
	for _, kind := range movements {
		mac := macWithTheCursorAt(whereTheCursorWas, everything)

		keptBefore := mac.mouseMoves(kind, 10, 0)
		mac.hold.take()
		mac.mouseMoves(kind, 40, 25)
		mac.hold.letGo()
		keptAfter := mac.mouseMoves(kind, 0, 7)

		if keptBefore || keptAfter {
			t.Errorf("movement of type %d kept from this Mac before the input was taken: %v, after it was given back: %v, want neither", kind, keptBefore, keptAfter)
		}
		if at, want := mac.pointer.Cursor(), (Point{X: whereTheCursorWas.X + 10, Y: whereTheCursorWas.Y + 7}); at != want {
			t.Errorf("after movement of type %d the cursor is at %+v, want %+v: free before and after, held in between", kind, at, want)
		}
		if times := mac.pointer.timesPutBack(); times != 1 {
			t.Errorf("movement of type %d put the cursor back %d times, want once, for the movement made while the input was taken", kind, times)
		}
	}
}

func TestCursorIsHeldWhereItIsEachTimeTheInputIsTaken(t *testing.T) {
	mac := macWithTheCursorAt(whereTheCursorWas, everything)
	mac.hold.take()
	mac.mouseMoves(TypeMouseMoved, 40, 25)
	mac.hold.letGo()
	mac.mouseMoves(TypeMouseMoved, -200, 60)
	second := mac.pointer.Cursor()

	mac.hold.take()
	mac.mouseMoves(TypeMouseMoved, 15, 15)

	if at := mac.pointer.Cursor(); at != second {
		t.Errorf("the cursor is at %+v, want it held at %+v, where it was when the input was taken again", at, second)
	}
}

func TestInputThatIsNotAcceptedPassesOnAndLeavesTheCursorAlone(t *testing.T) {
	mac := macWithTheCursorAt(whereTheCursorWas, notPostedByLanhop)
	mac.hold.take()
	mac.pointer.moveBy(40, 25)

	kept := mac.hold.keeps(Observation{Type: TypeMouseMoved, DX: 40, DY: 25, Marker: Marker})

	if kept {
		t.Error("a movement that lanhop itself posted was kept from this Mac")
	}
	if at, want := mac.pointer.Cursor(), (Point{X: whereTheCursorWas.X + 40, Y: whereTheCursorWas.Y + 25}); at != want {
		t.Errorf("the cursor is at %+v, want it at %+v, where the posted movement took it", at, want)
	}
}

func TestCursorPlacedWhileTheInputIsTakenIsHeldAtItsNewPlace(t *testing.T) {
	mac := macWithTheCursorAt(whereTheCursorWas, everything)
	arrival := Point{X: 1919, Y: 540}
	mac.hold.take()
	mac.mouseMoves(TypeMouseMoved, 40, 25)

	placeCursorOn(mac.hold, int(arrival.X), int(arrival.Y))
	placed := mac.pointer.Cursor()
	mac.mouseMoves(TypeMouseMoved, -30, 12)

	if placed != arrival {
		t.Errorf("the cursor was placed at %+v, want %+v", placed, arrival)
	}
	if at := mac.pointer.Cursor(); at != arrival {
		t.Errorf("a movement kept after the cursor was placed left it at %+v, want it held at %+v", at, arrival)
	}
}

func TestMovementInFlightNeverUndoesAPlacementFollowedByGivingTheInputBack(t *testing.T) {
	arrival := Point{X: 1919, Y: 540}
	for round := range 300 {
		mac := macWithTheCursorAt(whereTheCursorWas, everything)
		mac.hold.take()
		flying, landed := make(chan struct{}), make(chan struct{})
		stop := make(chan struct{})
		go func() {
			defer close(landed)
			mac.hold.keeps(Observation{Type: TypeMouseMoved, DX: 1})
			close(flying)
			for {
				select {
				case <-stop:
					return
				default:
					mac.hold.keeps(Observation{Type: TypeMouseMoved, DX: 1})
				}
			}
		}()
		<-flying

		placeCursorOn(mac.hold, int(arrival.X), int(arrival.Y))
		mac.hold.letGo()
		afterGivingBack := mac.pointer.Cursor()
		close(stop)
		<-landed

		if at := mac.pointer.Cursor(); afterGivingBack != arrival || at != arrival {
			t.Fatalf("round %d: the cursor was at %+v when the input was given back and at %+v in the end, want %+v both times", round, afterGivingBack, at, arrival)
		}
	}
}
