package sharer

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/edge"
	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/identity"
	"github.com/rodrigogaraujo/lanhop/internal/layout"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

const (
	thisMachine    = "me"
	betweenReports = 10 * time.Millisecond
	reportsToCross = 16
	centre         = 32767
	farEdge        = 65535
)

var (
	ownDesktop = edge.Bounds{MinX: 0, MinY: 0, MaxX: 1000, MaxY: 500}
	namesake   = trust.Peer{Name: thisMachine, Fingerprint: identity.Fingerprint{9}}
)

type fakeClock struct {
	mutex   sync.Mutex
	elapsed time.Duration
}

func (c *fakeClock) now() time.Time {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return time.Unix(0, 0).Add(c.elapsed)
}

func (c *fakeClock) advance(by time.Duration) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.elapsed += by
}

type fakeScreen struct {
	*fakeCapture
	mutex   sync.Mutex
	x, y    int
	desktop edge.Bounds
	lost    bool
}

func (f *fakeScreen) Cursor() (x, y int, desktop edge.Bounds, known bool) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.x, f.y, f.desktop, !f.lost
}

func (f *fakeScreen) PlaceCursor(x, y int) {
	f.mutex.Lock()
	f.x, f.y = x, y
	f.mutex.Unlock()
	f.journal.note(cursorPlacedAt(x, y))
}

func (f *fakeScreen) cursorIsAt(x, y int) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.x, f.y = x, y
}

func (f *fakeScreen) cursorCannotBeFound() {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.lost = true
}

func cursorPlacedAt(x, y int) string {
	return fmt.Sprintf("this machine: cursor placed at (%d, %d)", x, y)
}

func drawn(t *testing.T, desk string) *layout.Layout {
	t.Helper()
	parsed, err := layout.Parse(strings.NewReader(desk), func(string) bool { return true })
	if err != nil {
		t.Fatalf("the layout %q of the test has problems: %v", desk, err)
	}
	return parsed
}

func startSharingWithLayout(t *testing.T, desk string, peers ...trust.Peer) *sharing {
	t.Helper()
	s := newSharing(t, peers...)
	s.desk.Store(drawn(t, desk))
	s.startWithAScreen(ownDesktop)
	return s
}

func (s *sharing) startWithAScreen(desktop edge.Bounds) {
	s.t.Helper()
	s.screen = &fakeScreen{fakeCapture: s.capture, desktop: desktop}
	s.start(s.screen)
}

func startSharingWithLayoutButNoCursor(t *testing.T, desk string, peers ...trust.Peer) *sharing {
	t.Helper()
	s := newSharing(t, peers...)
	s.desk.Store(drawn(t, desk))
	s.start(s.capture)
	return s
}

func movedBy(dx, dy int) []event.Event {
	var report []event.Event
	if dx != 0 {
		report = append(report, event.Relative(event.RelX, int32(dx)))
	}
	if dy != 0 {
		report = append(report, event.Relative(event.RelY, int32(dy)))
	}
	return append(report, event.Sync())
}

func (s *sharing) moves(dx, dy int) {
	s.t.Helper()
	s.captures(movedBy(dx, dy)...)
	s.clock.advance(betweenReports)
}

func (s *sharing) pushes(dx, dy, reports int) {
	s.t.Helper()
	for range reports {
		s.moves(dx, dy)
	}
}

func positioned(x, y int) []event.Event {
	return []event.Event{event.Absolute(event.AbsX, int32(x)), event.Absolute(event.AbsY, int32(y)), event.Sync()}
}

func enteredAt(x, y int) []event.Event {
	return append([]event.Event{event.Enter()}, positioned(x, y)...)
}

func times(count int, events ...event.Event) []event.Event {
	repeated := make([]event.Event, 0, count*len(events))
	for range count {
		repeated = append(repeated, events...)
	}
	return repeated
}

func TestPushingThroughAnEdgeGivesFocusToTheNeighbourOnThatSideAndPushingBackReturnsIt(t *testing.T) {
	s := startSharingWithLayout(t, "me desk", desk)
	s.connect(desk)
	s.screen.cursorIsAt(1000, 125)
	typing := eachFollowedBySync(press(event.KeyA), release(event.KeyA))

	s.pushes(1, 0, reportsToCross)
	s.captures(typing...)
	s.pushes(-1, 0, reportsToCross)
	s.captures(typing...)

	s.expectJournal(story(
		then(tookInput),
		gets(desk, enteredAt(0, 16384)...),
		gets(desk, typing...),
		gets(desk, times(reportsToCross, positioned(0, 16384)...)...),
		gets(desk, event.Leave()),
		then(gaveInputBack, cursorPlacedAt(1000, 125)),
	))
	s.expectInputAtThisMachine()
}

func TestCursorArrivesOnTheOppositeEdgeAtTheSameProportionAlongIt(t *testing.T) {
	offCentre := edge.Bounds{MinX: -1000, MinY: -200, MaxX: 999, MaxY: 799}
	tests := []struct {
		name                       string
		desk                       string
		desktop                    edge.Bounds
		leavesAtX, leavesAtY       int
		pushX, pushY               int
		arrivesAtX, arrivesAtY     int
		comesBackToX, comesBackToY int
		pushBackX, pushBackY       int
	}{
		{"through the right edge", "me desk", ownDesktop, 1000, 125, 1, 0, 0, 16384, 1000, 125, -1, 0},
		{"through the left edge", "desk me", ownDesktop, 0, 375, -1, 0, farEdge, 49151, 0, 375, 1, 0},
		{"through the top edge", "desk\nme", ownDesktop, 250, 0, 0, -1, 16384, farEdge, 250, 0, 0, 1},
		{"through the bottom edge", "me\ndesk", ownDesktop, 750, 500, 0, 1, 49151, 0, 750, 500, 0, -1},
		{"from a desktop that does not start at zero", "me desk", offCentre, 999, 300, 1, 0, 0, 32800, 999, 300, -1, 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := newSharing(t, desk)
			s.desk.Store(drawn(t, test.desk))
			s.startWithAScreen(test.desktop)
			s.connect(desk)
			s.screen.cursorIsAt(test.leavesAtX, test.leavesAtY)

			s.pushes(test.pushX, test.pushY, reportsToCross)
			s.pushes(test.pushBackX, test.pushBackY, reportsToCross)

			s.expectJournal(story(
				then(tookInput),
				gets(desk, enteredAt(test.arrivesAtX, test.arrivesAtY)...),
				gets(desk, times(reportsToCross, positioned(test.arrivesAtX, test.arrivesAtY)...)...),
				gets(desk, event.Leave()),
				then(gaveInputBack, cursorPlacedAt(test.comesBackToX, test.comesBackToY)),
			))
		})
	}
}

func TestFiftyCrossingsBackAndForthArriveAtTheSamePlaceEveryTime(t *testing.T) {
	const crossings = 50
	s := startSharingWithLayout(t, "me desk", desk)
	s.connect(desk)
	s.screen.cursorIsAt(1000, 333)
	roundTrip := story(
		then(tookInput),
		gets(desk, enteredAt(0, 43646)...),
		gets(desk, times(reportsToCross, positioned(0, 43646)...)...),
		gets(desk, event.Leave()),
		then(gaveInputBack, cursorPlacedAt(1000, 333)),
	)
	var want []string

	for range crossings {
		s.pushes(1, 0, reportsToCross)
		s.pushes(-1, 0, reportsToCross)
		want = append(want, roundTrip...)
	}

	s.expectJournal(want)
}

func TestEdgeWithNoNeighbourBehavesAsAnEdgeOfTheScreen(t *testing.T) {
	t.Run("on the screen of the sharer", func(t *testing.T) {
		s := startSharingWithLayout(t, "me desk", desk)
		s.connect(desk)
		s.screen.cursorIsAt(0, 125)

		s.pushes(-1, 0, 2*reportsToCross)

		s.expectJournal(nil)
		s.expectInputAtThisMachine()
	})
	t.Run("on the screen of a receiver", func(t *testing.T) {
		s := startSharingWithLayout(t, "me desk", desk)
		s.connect(desk)
		s.focusOn(2)

		s.moves(400, 0)
		s.pushes(1, 0, 2*reportsToCross)

		s.expectJournal(story(
			then(tookInput),
			gets(desk, enteredAt(centre, centre)...),
			gets(desk, times(1+2*reportsToCross, positioned(farEdge, centre)...)...),
		))
	})
}

func TestNeighbourThatIsNotConnectedIsNeitherEnteredNorJumpedOver(t *testing.T) {
	t.Run("from the screen of the sharer", func(t *testing.T) {
		s := startSharingWithLayout(t, "me laptop desk", desk, laptop)
		s.connect(desk)
		s.screen.cursorIsAt(1000, 125)

		s.pushes(1, 0, 2*reportsToCross)

		s.expectJournal(nil)
		s.expectInputAtThisMachine()
	})
	t.Run("from the screen of a receiver", func(t *testing.T) {
		s := startSharingWithLayout(t, "desk laptop me", desk, laptop)
		s.connect(desk)
		s.focusOn(2)

		s.moves(400, 0)
		s.pushes(1, 0, 2*reportsToCross)

		s.expectJournal(story(
			then(tookInput),
			gets(desk, enteredAt(centre, centre)...),
			gets(desk, times(1+2*reportsToCross, positioned(farEdge, centre)...)...),
		))
	})
}

func TestQuickTouchOfAnEdgeDoesNotCrossButAPushHeldLongEnoughDoes(t *testing.T) {
	s := startSharingWithLayout(t, "me desk", desk)
	s.connect(desk)
	s.screen.cursorIsAt(1000, 125)

	s.pushes(1, 0, reportsToCross-1)
	s.moves(-1, 0)
	s.pushes(1, 0, reportsToCross-1)
	s.expectJournal(nil)

	s.moves(1, 0)
	s.expectJournal(story(
		then(tookInput),
		gets(desk, enteredAt(0, 16384)...),
	))
}

func TestPushWithAButtonHeldDoesNotCross(t *testing.T) {
	t.Run("on the screen of the sharer", func(t *testing.T) {
		s := startSharingWithLayout(t, "me desk", desk)
		s.connect(desk)
		s.screen.cursorIsAt(1000, 125)

		s.captures(press(event.ButtonLeft), event.Sync())
		s.pushes(1, 0, 2*reportsToCross+reportsToCross/2)
		s.captures(release(event.ButtonLeft), event.Sync())
		s.pushes(1, 0, reportsToCross-1)
		s.expectJournal(nil)

		s.moves(1, 0)
		s.expectJournal(story(
			then(tookInput),
			gets(desk, enteredAt(0, 16384)...),
		))
	})
	t.Run("on the screen of a receiver", func(t *testing.T) {
		s := startSharingWithLayout(t, "desk me", desk)
		s.connect(desk)
		s.focusOn(2)
		dragging := []event.Event{press(event.ButtonLeft), event.Sync()}
		dropping := []event.Event{release(event.ButtonLeft), event.Sync()}

		s.captures(dragging...)
		s.moves(400, 0)
		s.pushes(1, 0, 2*reportsToCross)
		s.captures(dropping...)
		s.pushes(1, 0, reportsToCross)

		s.expectJournal(story(
			then(tookInput),
			gets(desk, enteredAt(centre, centre)...),
			gets(desk, dragging...),
			gets(desk, times(1+2*reportsToCross, positioned(farEdge, centre)...)...),
			gets(desk, dropping...),
			gets(desk, times(reportsToCross, positioned(farEdge, centre)...)...),
			gets(desk, event.Leave()),
			then(gaveInputBack, cursorPlacedAt(0, 250)),
		))
	})
}

func TestPushWithAKeyHeldOnTheSharerDoesNotLeaveItSoTheKeyIsNotLeftHeldThere(t *testing.T) {
	s := startSharingWithLayout(t, "me desk", desk)
	s.connect(desk)
	s.screen.cursorIsAt(1000, 125)

	s.captures(press(keyLeftShift), event.Sync())
	s.pushes(1, 0, 2*reportsToCross)
	s.expectJournal(nil)
	s.expectInputAtThisMachine()

	s.captures(release(keyLeftShift), event.Sync())
	s.pushes(1, 0, reportsToCross)
	s.expectJournal(story(
		then(tookInput),
		gets(desk, enteredAt(0, 16384)...),
	))
}

func TestCrossingWithAKeyHeldOnAReceiverReleasesItThereBeforeLeaving(t *testing.T) {
	s := startSharingWithLayout(t, "desk me", desk)
	s.connect(desk)
	s.focusOn(2)
	holding := []event.Event{press(keyLeftShift), event.Sync()}

	s.captures(holding...)
	s.moves(400, 0)
	s.pushes(1, 0, reportsToCross-1)

	s.expectJournal(story(
		then(tookInput),
		gets(desk, enteredAt(centre, centre)...),
		gets(desk, holding...),
		gets(desk, times(reportsToCross, positioned(farEdge, centre)...)...),
		gets(desk, release(keyLeftShift), event.Sync(), event.Leave()),
		then(gaveInputBack, cursorPlacedAt(0, 250)),
	))
}

func TestCrossingFromOneReceiverToAnotherLeavesTheFirstAndEntersTheSecondOnItsOppositeEdge(t *testing.T) {
	s := startSharingWithLayout(t, "me desk laptop", desk, laptop)
	s.connect(desk)
	s.connect(laptop)
	s.focusOn(2)
	typing := eachFollowedBySync(press(event.KeyA), release(event.KeyA))

	s.moves(400, 100)
	s.pushes(1, 0, reportsToCross-1)
	s.captures(typing...)
	s.pushes(-1, 0, reportsToCross)

	s.expectJournal(story(
		then(tookInput),
		gets(desk, enteredAt(centre, centre)...),
		gets(desk, times(reportsToCross, positioned(farEdge, 42767)...)...),
		gets(desk, event.Leave()),
		gets(laptop, enteredAt(0, 42767)...),
		gets(laptop, typing...),
		gets(laptop, times(reportsToCross, positioned(0, 42767)...)...),
		gets(laptop, event.Leave()),
		gets(desk, enteredAt(farEdge, 42767)...),
	))
}

func TestCrossingFromOneReceiverToAnotherWithAKeyHeldReleasesItOnTheFirstBeforeLeaving(t *testing.T) {
	s := startSharingWithLayout(t, "me desk laptop", desk, laptop)
	s.connect(desk)
	s.connect(laptop)
	s.focusOn(2)
	holding := []event.Event{press(keyLeftShift), event.Sync()}

	s.captures(holding...)
	s.moves(400, 0)
	s.pushes(1, 0, reportsToCross-1)

	s.expectJournal(story(
		then(tookInput),
		gets(desk, enteredAt(centre, centre)...),
		gets(desk, holding...),
		gets(desk, times(reportsToCross, positioned(farEdge, centre)...)...),
		gets(desk, release(keyLeftShift), event.Sync(), event.Leave()),
		gets(laptop, enteredAt(0, centre)...),
	))
}

func TestReceiverNamedAsTheSharerIsNotTheMachineTheLayoutNamesSoItsEdgesDoNotCross(t *testing.T) {
	s := startSharingWithLayout(t, "me desk", namesake, desk)
	s.connect(namesake)
	s.connect(desk)
	s.focusOn(2)

	s.moves(400, 0)
	s.pushes(1, 0, 2*reportsToCross)

	s.expectJournal(story(
		then(tookInput),
		gets(namesake, enteredAt(centre, centre)...),
		gets(namesake, times(1+2*reportsToCross, positioned(farEdge, centre)...)...),
	))
}

func TestCornerOfAReceiverCoversTheSameShareOfItsEdgeAsOnAScreenOf1080Lines(t *testing.T) {
	s := newSharing(t, desk, laptop)
	s.desk.Store(drawn(t, "me desk laptop"))
	s.speed = Speed{X: 100, Y: 1}
	s.startWithAScreen(ownDesktop)
	s.connect(desk)
	s.connect(laptop)
	s.focusOn(2)

	s.moves(400, 1900-centre)
	s.pushes(1, 0, 2*reportsToCross)
	s.moves(0, 100)
	s.pushes(1, 0, reportsToCross)

	s.expectJournal(story(
		then(tookInput),
		gets(desk, enteredAt(centre, centre)...),
		gets(desk, times(1+2*reportsToCross, positioned(farEdge, 1900)...)...),
		gets(desk, times(1+reportsToCross, positioned(farEdge, 2000)...)...),
		gets(desk, event.Leave()),
		gets(laptop, enteredAt(0, 2000)...),
	))
}

func TestMovementBecomesPositionsOfTheCursorTheSharerKeepsForTheReceiver(t *testing.T) {
	type point struct{ x, y int }
	tests := []struct {
		name    string
		speed   Speed
		reports []point
		want    []point
	}{
		{
			name:    "scaled by the speed of each axis",
			speed:   Speed{X: 100, Y: 10},
			reports: []point{{3, -2}, {-1, 0}, {0, 5}},
			want:    []point{{33067, 32747}, {32967, 32747}, {32967, 32797}},
		},
		{
			name:    "with the speed that assumes a desktop of 1920 by 1080",
			speed:   DefaultSpeed(),
			reports: []point{{3, -1}, {1920, 0}},
			want:    []point{{32869, 32706}, {farEdge, 32706}},
		},
		{
			name:    "keeping the fraction that does not make a whole unit",
			speed:   Speed{X: 0.4, Y: 0.25},
			reports: []point{{1, 1}, {1, 1}, {1, 1}, {1, 1}, {1, 1}},
			want:    []point{{32767, 32767}, {32768, 32768}, {32768, 32768}, {32769, 32768}, {32769, 32768}},
		},
		{
			name:    "clamped to the range, without remembering what went past the edge",
			speed:   Speed{X: 100, Y: 100},
			reports: []point{{-400, 400}, {-7, 7}, {1, -1}, {700, -700}, {-1, 1}},
			want:    []point{{0, farEdge}, {0, farEdge}, {100, 65435}, {farEdge, 0}, {65435, 100}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := newSharing(t, desk)
			s.desk.Store(drawn(t, "desk"))
			s.speed = test.speed
			s.startWithAScreen(ownDesktop)
			s.connect(desk)
			s.focusOn(2)

			var want []event.Event
			for i, report := range test.reports {
				s.moves(report.x, report.y)
				want = append(want, positioned(test.want[i].x, test.want[i].y)...)
			}

			s.expectJournal(story(
				then(tookInput),
				gets(desk, enteredAt(centre, centre)...),
				gets(desk, want...),
			))
		})
	}
}

func TestWheelKeysAndButtonsAreSentAsTheyAreNextToThePositions(t *testing.T) {
	s := startSharingWithLayout(t, "me desk", desk)
	s.connect(desk)
	s.focusOn(2)

	s.captures(event.Relative(event.RelX, 2), event.Relative(event.RelWheel, -1), press(event.ButtonLeft), event.Sync())
	s.captures(event.Relative(event.RelHWheel, 3), event.Sync())
	s.captures(release(event.ButtonLeft), press(event.KeyA), event.Sync())
	s.captures(release(event.KeyA), event.Relative(event.RelY, -1), event.Sync())

	s.expectJournal(story(
		then(tookInput),
		gets(desk, enteredAt(centre, centre)...),
		gets(desk, event.Absolute(event.AbsX, 32967), event.Absolute(event.AbsY, centre), event.Relative(event.RelWheel, -1), press(event.ButtonLeft), event.Sync()),
		gets(desk, event.Relative(event.RelHWheel, 3), event.Sync()),
		gets(desk, release(event.ButtonLeft), press(event.KeyA), event.Sync()),
		gets(desk, event.Absolute(event.AbsX, 32967), event.Absolute(event.AbsY, 32667), release(event.KeyA), event.Sync()),
	))
}

func TestWithoutALayoutMovementIsForwardedAsItIsAndEdgesDoNotCross(t *testing.T) {
	s := newSharing(t, desk)
	s.startWithAScreen(ownDesktop)
	s.connect(desk)
	s.screen.cursorIsAt(1000, 125)

	s.pushes(1, 0, 2*reportsToCross)
	s.focusOn(2)
	s.pushes(-700, 0, 2*reportsToCross)

	s.expectJournal(story(
		then(tookInput),
		gets(desk, event.Enter()),
		gets(desk, times(2*reportsToCross, event.Relative(event.RelX, -700), event.Sync())...),
	))
}

func TestLayoutThatChangesWhileSharingAppliesToTheNextCrossing(t *testing.T) {
	s := newSharing(t, desk, laptop)
	s.startWithAScreen(ownDesktop)
	s.connect(desk)
	s.connect(laptop)
	s.screen.cursorIsAt(1000, 125)

	s.pushes(1, 0, reportsToCross)
	s.desk.Store(drawn(t, "me desk"))
	s.pushes(1, 0, reportsToCross)
	s.focusOn(1)
	s.desk.Store(drawn(t, "me laptop"))
	s.pushes(1, 0, reportsToCross)
	s.focusOn(1)
	s.desk.Store(nil)
	s.pushes(1, 0, reportsToCross)
	s.focusOn(3)

	s.expectJournal(story(
		then(tookInput),
		gets(desk, enteredAt(0, 16384)...),
		gets(desk, hotkeyAsTheFocusedReceiverSeesIt()...),
		gets(desk, event.Leave()),
		then(gaveInputBack, tookInput),
		gets(laptop, enteredAt(0, 16384)...),
		gets(laptop, hotkeyAsTheFocusedReceiverSeesIt()...),
		gets(laptop, event.Leave()),
		then(gaveInputBack, tookInput),
		gets(laptop, event.Enter()),
	))
}

func TestLayoutThatAppearsWhileAReceiverHasFocusAppliesFromItsNextVisit(t *testing.T) {
	s := newSharing(t, desk)
	s.startWithAScreen(ownDesktop)
	s.connect(desk)
	s.focusOn(2)

	s.desk.Store(drawn(t, "desk me"))
	s.pushes(700, 0, reportsToCross)
	s.focusOn(1)
	s.focusOn(2)
	s.moves(400, 0)
	s.pushes(1, 0, reportsToCross-1)

	s.expectJournal(story(
		then(tookInput),
		gets(desk, event.Enter()),
		gets(desk, times(reportsToCross, event.Relative(event.RelX, 700), event.Sync())...),
		gets(desk, hotkeyAsTheFocusedReceiverSeesIt()...),
		gets(desk, event.Leave()),
		then(gaveInputBack, tookInput),
		gets(desk, enteredAt(centre, centre)...),
		gets(desk, times(reportsToCross, positioned(farEdge, centre)...)...),
		gets(desk, event.Leave()),
		then(gaveInputBack, cursorPlacedAt(0, 250)),
	))
}

func TestLayoutThatGoesAwayLeavesTheNextVisitWithMovementForwardedAsItIs(t *testing.T) {
	t.Run("after the receiver was left with the hotkey", func(t *testing.T) {
		s := startSharingWithLayout(t, "me desk", desk)
		s.connect(desk)
		s.focusOn(2)
		s.focusOn(1)

		s.desk.Store(nil)
		s.focusOn(2)
		s.moves(7, 0)

		s.expectJournal(story(
			then(tookInput),
			gets(desk, enteredAt(centre, centre)...),
			gets(desk, hotkeyAsTheFocusedReceiverSeesIt()...),
			gets(desk, event.Leave()),
			then(gaveInputBack, tookInput),
			gets(desk, event.Enter()),
			gets(desk, event.Relative(event.RelX, 7), event.Sync()),
		))
	})
	t.Run("after the receiver was lost while it had focus", func(t *testing.T) {
		s := startSharingWithLayout(t, "me desk", desk)
		lost := s.connect(desk)
		s.focusOn(2)
		lost.loseConnection()
		s.eventually("giving the input back", func() bool { return s.journal.has(gaveInputBack) })

		s.desk.Store(nil)
		s.connect(desk)
		s.focusOn(2)
		s.moves(7, 0)

		s.expectJournal(story(
			then(tookInput),
			gets(desk, enteredAt(centre, centre)...),
			then(closed(desk), gaveInputBack, tookInput),
			gets(desk, event.Enter()),
			gets(desk, event.Relative(event.RelX, 7), event.Sync()),
		))
	})
}

func TestHotkeyWorksInEveryStateOfEdgeSwitching(t *testing.T) {
	s := startSharingWithLayout(t, "me desk laptop", desk, laptop)
	s.connect(desk)
	s.connect(laptop)
	s.screen.cursorIsAt(1000, 125)

	s.focusOn(2)
	s.focusOn(3)
	s.focusOn(1)
	s.pushes(1, 0, reportsToCross)
	s.focusOn(3)
	s.moves(-400, 0)
	s.pushes(-1, 0, reportsToCross-1)
	s.focusOn(1)
	s.pushes(1, 0, reportsToCross)
	s.focusOn(1)

	s.expectJournal(story(
		then(tookInput),
		gets(desk, enteredAt(centre, centre)...),
		gets(desk, hotkeyAsTheFocusedReceiverSeesIt()...),
		gets(desk, event.Leave()),
		gets(laptop, enteredAt(centre, centre)...),
		gets(laptop, hotkeyAsTheFocusedReceiverSeesIt()...),
		gets(laptop, event.Leave()),
		then(gaveInputBack, tookInput),
		gets(desk, enteredAt(0, 16384)...),
		gets(desk, hotkeyAsTheFocusedReceiverSeesIt()...),
		gets(desk, event.Leave()),
		gets(laptop, enteredAt(centre, centre)...),
		gets(laptop, times(reportsToCross, positioned(0, centre)...)...),
		gets(laptop, event.Leave()),
		gets(desk, enteredAt(farEdge, centre)...),
		gets(desk, hotkeyAsTheFocusedReceiverSeesIt()...),
		gets(desk, event.Leave()),
		then(gaveInputBack, tookInput),
		gets(desk, enteredAt(0, 16384)...),
		gets(desk, hotkeyAsTheFocusedReceiverSeesIt()...),
		gets(desk, event.Leave()),
		then(gaveInputBack),
	))
	s.expectInputAtThisMachine()
}

func TestReceiverLostWhileItHasFocusThroughAnEdgeGivesTheInputBackAndItsCursorStartsOver(t *testing.T) {
	s := startSharingWithLayout(t, "me desk", desk)
	lost := s.connect(desk)
	s.screen.cursorIsAt(1000, 125)
	s.pushes(1, 0, reportsToCross)
	s.moves(5, 0)

	lost.loseConnection()
	s.eventually("giving the input back", func() bool { return s.journal.has(gaveInputBack) })
	s.pushes(-1, 0, reportsToCross)
	s.connect(desk)
	s.focusOn(2)
	s.moves(1, 0)

	s.expectJournal(story(
		then(tookInput),
		gets(desk, enteredAt(0, 16384)...),
		gets(desk, positioned(500, 16384)...),
		then(closed(desk), gaveInputBack, tookInput),
		gets(desk, enteredAt(centre, centre)...),
		gets(desk, positioned(32867, centre)...),
	))
}

func TestReceiverThatCannotBeEnteredThroughAnEdgeIsLostAndTheInputIsGivenBack(t *testing.T) {
	s := startSharingWithLayout(t, "me desk", desk)
	receiver := s.connect(desk)
	s.screen.cursorIsAt(1000, 125)
	receiver.sendFails.Store(true)

	s.pushes(1, 0, reportsToCross)

	s.expectJournal(then(tookInput, gaveInputBack, closed(desk)))
	s.expectInputAtThisMachine()
}

func TestSharerStopsWithoutPlacingTheCursorWhenTheInputCannotBeGivenBackOnTheWayBackThroughAnEdge(t *testing.T) {
	s := startSharingWithLayout(t, "me desk", desk)
	s.connect(desk)
	s.screen.cursorIsAt(1000, 125)
	s.pushes(1, 0, reportsToCross)
	s.capture.giveBackFails.Store(true)

	s.pushes(-1, 0, reportsToCross)
	result := s.outcome()

	if !errors.Is(result.err, errGiveBackFailed) {
		t.Errorf("the sharer stopped with %+v, want %v", result, errGiveBackFailed)
	}
	s.expectJournal(story(
		then(tookInput),
		gets(desk, enteredAt(0, 16384)...),
		gets(desk, times(reportsToCross, positioned(0, 16384)...)...),
		gets(desk, event.Leave()),
		then(gaveInputBack, gaveInputBack, closed(desk)),
	))
}

func TestReceiverThatCannotBeSentAPositionIsLostAndTheSharerKeepsWorking(t *testing.T) {
	s := startSharingWithLayout(t, "me desk", desk)
	receiver := s.connect(desk)
	s.focusOn(2)
	receiver.sendFails.Store(true)

	s.moves(5, 0)
	s.moves(5, 0)
	s.expectInputAtThisMachine()
	s.connect(desk)
	s.focusOn(2)

	s.expectJournal(story(
		then(tookInput),
		gets(desk, enteredAt(centre, centre)...),
		then(gaveInputBack, closed(desk), tookInput),
		gets(desk, enteredAt(centre, centre)...),
	))
}

func TestSharerWhoseCaptureCannotTellTheCursorCrossesOnlyTheEdgesOfItsReceivers(t *testing.T) {
	s := startSharingWithLayoutButNoCursor(t, "desk me", desk)
	s.connect(desk)

	s.pushes(-1, 0, 2*reportsToCross)
	s.focusOn(2)
	s.moves(400, 0)
	s.pushes(1, 0, reportsToCross-1)

	s.expectJournal(story(
		then(tookInput),
		gets(desk, enteredAt(centre, centre)...),
		gets(desk, times(reportsToCross, positioned(farEdge, centre)...)...),
		gets(desk, event.Leave()),
		then(gaveInputBack),
	))
	s.expectInputAtThisMachine()
}

func TestSharerWhoseCursorCannotBeFoundRightNowDoesNotCrossItsOwnEdgesNorPlaceTheCursor(t *testing.T) {
	s := startSharingWithLayout(t, "desk me", desk)
	s.connect(desk)
	s.screen.cursorIsAt(0, 125)
	s.screen.cursorCannotBeFound()

	s.pushes(-1, 0, 2*reportsToCross)
	s.focusOn(2)
	s.moves(400, 0)
	s.pushes(1, 0, reportsToCross-1)

	s.expectJournal(story(
		then(tookInput),
		gets(desk, enteredAt(centre, centre)...),
		gets(desk, times(reportsToCross, positioned(farEdge, centre)...)...),
		gets(desk, event.Leave()),
		then(gaveInputBack),
	))
}

func TestSharerTellsWhetherItsOwnScreenHasEdges(t *testing.T) {
	t.Run("a capture that tells the cursor has them", func(t *testing.T) {
		s := startSharingWithLayout(t, "me desk", desk)

		if !s.sharer.OwnScreenHasEdges() {
			t.Error("the screen of a sharer that can find its cursor has no edges")
		}
	})
	t.Run("a capture that cannot find the cursor has none", func(t *testing.T) {
		s := startSharingWithLayout(t, "me desk", desk)
		s.screen.cursorCannotBeFound()

		if s.sharer.OwnScreenHasEdges() {
			t.Error("the screen of a sharer that cannot find its cursor has edges")
		}
	})
	t.Run("a capture without the ability has none", func(t *testing.T) {
		s := startSharingWithLayoutButNoCursor(t, "me desk", desk)

		if s.sharer.OwnScreenHasEdges() {
			t.Error("the screen of a sharer whose capture cannot tell the cursor has edges")
		}
	})
}
