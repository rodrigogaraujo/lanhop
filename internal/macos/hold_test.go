package macos

import (
	"slices"
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
	hidden   int
	hides    int
	shows    int
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

func (p *fakePointer) HideCursor() {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.hidden++
	p.hides++
}

func (p *fakePointer) ShowCursor() {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.hidden = max(p.hidden-1, 0)
	p.shows++
}

func (p *fakePointer) visibility() (hidden bool, hides, shows int) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.hidden > 0, p.hides, p.shows
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
	return &macWithAMouse{pointer: pointer, hold: &hold{holdingScreen: pointer, accepts: accepts, hotkey: newHotkeyGuard(HotkeyFlags)}}
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

func TestDigitOfTheHotkeyIsKeptWhileThisMacHasTheInputAndItsModifiersPassOn(t *testing.T) {
	mac := macWithTheCursorAt(whereTheCursorWas, everything)

	var kept []bool
	for _, seen := range hotkeyOnARealKeyboard(macOSDigit2) {
		kept = append(kept, mac.hold.keeps(seen))
	}

	if want := []bool{false, false, true, true, false, false}; !slices.Equal(kept, want) {
		t.Errorf("of Control, Option, the digit going down and up, Option and Control, kept %v from this Mac, want %v", kept, want)
	}
}

func TestDigitOfTheHotkeyStaysKeptWhenTheInputIsGivenBackBeforeItsRelease(t *testing.T) {
	chord := FlagControl | FlagOption
	mac := macWithTheCursorAt(whereTheCursorWas, everything)
	mac.hold.take()
	mac.hold.keeps(Observation{Type: TypeKeyDown, KeyCode: macOSDigit1, Flags: chord})
	mac.hold.letGo()

	keptRepeat := mac.hold.keeps(Observation{Type: TypeKeyDown, KeyCode: macOSDigit1, Flags: chord, Repeat: true})
	keptRelease := mac.hold.keeps(Observation{Type: TypeKeyUp, KeyCode: macOSDigit1, Flags: chord})
	keptLetter := mac.hold.keeps(Observation{Type: TypeKeyDown, KeyCode: macOSKeyA, Flags: chord})

	if !keptRepeat || !keptRelease {
		t.Errorf("after the input was given back the repeat of the digit was kept: %v, its release: %v, want both kept, as its press was", keptRepeat, keptRelease)
	}
	if keptLetter {
		t.Error("a letter typed after the input was given back was kept from this Mac")
	}
}

func TestDigitOfTheHotkeyThatLanhopPostedPassesOnAndKeepsNothingAfterIt(t *testing.T) {
	chord := FlagControl | FlagOption
	mac := macWithTheCursorAt(whereTheCursorWas, notPostedByLanhop)

	keptPosted := mac.hold.keeps(Observation{Type: TypeKeyDown, KeyCode: macOSDigit2, Flags: chord, Marker: Marker})
	keptReleaseByTheUser := mac.hold.keeps(Observation{Type: TypeKeyUp, KeyCode: macOSDigit2})

	if keptPosted {
		t.Error("the digit of a hotkey that lanhop itself posted was kept from this Mac")
	}
	if keptReleaseByTheUser {
		t.Error("the release of a digit by the user was kept from this Mac after lanhop posted that digit with the hotkey")
	}
}

func TestCursorIsHiddenWhileTheInputIsTakenAndShownWhenItIsGivenBack(t *testing.T) {
	mac := macWithTheCursorAt(whereTheCursorWas, everything)

	if hidden, hides, shows := mac.pointer.visibility(); hidden || hides != 0 || shows != 0 {
		t.Fatalf("before the input is taken: hidden %v, hides %d, shows %d, want a visible cursor nobody touched", hidden, hides, shows)
	}
	mac.hold.take()
	mac.hold.take()
	if hidden, hides, _ := mac.pointer.visibility(); !hidden || hides != 1 {
		t.Errorf("with the input taken twice: hidden %v after %d hides, want it hidden once", hidden, hides)
	}
	mac.hold.letGo()
	mac.hold.letGo()
	if hidden, _, shows := mac.pointer.visibility(); hidden || shows != 1 {
		t.Errorf("with the input given back twice: hidden %v after %d shows, want it shown once", hidden, shows)
	}
}

func TestGivingBackInputThatWasNotTakenDoesNotShowTheCursor(t *testing.T) {
	mac := macWithTheCursorAt(whereTheCursorWas, everything)

	mac.hold.letGo()

	if _, hides, shows := mac.pointer.visibility(); hides != 0 || shows != 0 {
		t.Errorf("hides %d, shows %d, want the cursor left alone", hides, shows)
	}
}

func TestCursorIsHiddenAgainEachTimeTheInputIsTaken(t *testing.T) {
	mac := macWithTheCursorAt(whereTheCursorWas, everything)

	for visit := 1; visit <= 3; visit++ {
		mac.hold.take()
		if hidden, hides, _ := mac.pointer.visibility(); !hidden || hides != visit {
			t.Errorf("input taken for the time number %d: hidden %v after %d hides, want it hidden once more", visit, hidden, hides)
		}
		mac.hold.letGo()
		if hidden, _, shows := mac.pointer.visibility(); hidden || shows != visit {
			t.Errorf("input given back for the time number %d: hidden %v after %d shows, want it shown once more", visit, hidden, shows)
		}
	}
}

func TestCursorStaysHiddenWhileItIsHeldInPlaceAndWhenItIsPlaced(t *testing.T) {
	mac := macWithTheCursorAt(whereTheCursorWas, everything)
	mac.hold.take()

	for _, kind := range movements {
		mac.mouseMoves(kind, 40, 25)
	}
	placeCursorOn(mac.hold, 1919, 540)
	mac.mouseMoves(TypeMouseMoved, -30, 12)

	if hidden, hides, shows := mac.pointer.visibility(); !hidden || hides != 1 || shows != 0 {
		t.Errorf("after movement and a placement with the input taken: hidden %v, hides %d, shows %d, want the one hide of taking it and nothing else", hidden, hides, shows)
	}
}

func TestEveryHideIsUndoneWhenTheInputIsTakenAndGivenBackFromSeveralGoroutinesAtOnce(t *testing.T) {
	mac := macWithTheCursorAt(whereTheCursorWas, everything)
	var busy sync.WaitGroup
	for _, act := range []func(){
		mac.hold.take,
		mac.hold.letGo,
		mac.hold.take,
		mac.hold.letGo,
		func() { placeCursorOn(mac.hold, 1919, 540) },
		func() { mac.hold.keeps(Observation{Type: TypeMouseMoved, DX: 1}) },
	} {
		busy.Add(1)
		go func() {
			defer busy.Done()
			for range 2000 {
				act()
			}
		}()
	}
	busy.Wait()

	mac.hold.letGo()

	if hidden, hides, shows := mac.pointer.visibility(); hidden || hides != shows {
		t.Errorf("after the last time the input was given back: hidden %v, hides %d, shows %d, want a visible cursor and a show for every hide", hidden, hides, shows)
	}
}
