package macos

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type followedWindow struct {
	mu       sync.Mutex
	screen   *dropScreen
	takesOn  int
	tries    int
	failWith error
}

func (w *followedWindow) takeTheMouseAt(at Point) (bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.tries++
	w.screen.moveTo(Point{X: at.X + 5, Y: at.Y + 1})
	if w.takesOn == 0 || w.tries < w.takesOn {
		return false, w.failWith
	}
	return true, nil
}

func startUnderTheCursorOn(screen *dropScreen, window cursorFollower, answers appKitAnswers) (startUnderTheCursor, *heldDown) {
	button := newHeldDownByThePerson(screen)
	downSeen, began := newLatch(), newLatch()
	screen.onPost = func(mouse MouseEvent) {
		if mouse.Action == MouseDown && answers.seesDown {
			downSeen.fire()
		}
		if mouse.Action == MouseDragged && answers.begins {
			began.fire()
		}
	}
	return startUnderTheCursor{
		button:   button,
		window:   window,
		cursor:   screen.Cursor,
		plan:     func(at Point) dragPlan { return planDrag(at, []Rect{macBookDisplay}) },
		downSeen: downSeen.done,
		began:    began.done,
		timing:   quickTiming,
		limit:    150 * time.Millisecond,
	}, button
}

func TestAStartUnderTheCursorFollowsTheMovingCursorAndPressesWhereTheWindowTookIt(t *testing.T) {
	screen := &dropScreen{cursor: leftEdge}
	window := &followedWindow{screen: screen, takesOn: 3}
	start, _ := startUnderTheCursorOn(screen, window, appKitAnswers{seesDown: true, begins: true})

	if err := start.run(context.Background()); err != nil {
		t.Fatalf("the start failed: %v", err)
	}

	tookAt := Point{X: leftEdge.X + 10, Y: leftEdge.Y + 2}
	expectEvents(t, screen.events(), append([]MouseEvent{postedDown(tookAt)}, stepsRightOf(tookAt)...)...)
}

func TestAStartUnderTheCursorThatNeverTakesTheMouseFailsWithinItsLimitAndPostsNothing(t *testing.T) {
	screen := &dropScreen{cursor: leftEdge}
	window := &followedWindow{screen: screen, failWith: ErrMainThreadLate}
	start, _ := startUnderTheCursorOn(screen, window, appKitAnswers{seesDown: true, begins: true})

	began := time.Now()
	err := start.run(context.Background())

	if !errors.Is(err, ErrDropWindowMissed) {
		t.Errorf("the start gave %v, want ErrDropWindowMissed", err)
	}
	if took := time.Since(began); took > time.Second {
		t.Errorf("the start took %v, longer than its limit", took)
	}
	expectEvents(t, screen.events())
}

func TestAStartUnderTheCursorNeverPostsAnUpForTheButtonThePersonHolds(t *testing.T) {
	for name, answers := range map[string]appKitAnswers{
		"the down is not seen":   {},
		"no session begins":      {seesDown: true},
		"the session then stops": {seesDown: true, begins: true},
	} {
		t.Run(name, func(t *testing.T) {
			screen := &dropScreen{cursor: leftEdge}
			window := &followedWindow{screen: screen, takesOn: 1}
			start, button := startUnderTheCursorOn(screen, window, answers)

			start.run(context.Background())
			button.releaseAtHome()
			button.releaseOverTheWindowAt(leftEdge)
			button.escapeThenReleaseAt(leftEdge)

			for _, posted := range screen.events() {
				if posted.Action == MouseUp {
					t.Errorf("an up was posted for the button the person holds: %+v", screen.events())
				}
			}
		})
	}
}

func TestTheButtonThePersonHoldsIsLetGoOverTheWindowWithoutAnUp(t *testing.T) {
	screen := &dropScreen{cursor: leftEdge}
	button := newHeldDownByThePerson(screen)
	button.pressAt(leftEdge)
	elsewhere := Point{X: 300, Y: 200}

	if !button.releaseOverTheWindowAt(elsewhere) {
		t.Fatal("the button was not let go")
	}

	expectEvents(t, screen.events(), postedDown(leftEdge), MouseEvent{Action: MouseDragged, At: elsewhere, Clicks: 1})
	if button.pending() {
		t.Error("the button is still pending after it was let go")
	}
}
