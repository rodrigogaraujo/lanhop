package sharer

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/edge"
	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/hotkey"
	"github.com/rodrigogaraujo/lanhop/internal/identity"
	"github.com/rodrigogaraujo/lanhop/internal/layout"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

const (
	testPatience      = 5 * time.Second
	testTrustCheck    = 2 * time.Millisecond
	keyLeftCtrl       = uint16(29)
	keyLeftAlt        = uint16(56)
	keyLeftShift      = uint16(42)
	keyB              = uint16(48)
	typeMiscellaneous = uint16(0x04)
	syncDropped       = uint16(0x03)
	tookInput         = "this machine: input taken away"
	gaveInputBack     = "this machine: input given back"
)

var (
	desk    = trust.Peer{Name: "desk", Fingerprint: identity.Fingerprint{1}}
	laptop  = trust.Peer{Name: "laptop", Fingerprint: identity.Fingerprint{2}}
	tablet  = trust.Peer{Name: "tablet", Fingerprint: identity.Fingerprint{3}}
	ignored = event.Event{Type: typeMiscellaneous, Code: 4, Value: 458756}
)

func press(code uint16) event.Event   { return event.Key(code, event.KeyPressed) }
func release(code uint16) event.Event { return event.Key(code, event.KeyReleased) }
func repeat(code uint16) event.Event  { return event.Key(code, event.KeyRepeated) }
func digit(machine int) uint16        { return uint16(machine) + 1 }

func eachFollowedBySync(events ...event.Event) []event.Event {
	reports := make([]event.Event, 0, 2*len(events))
	for _, e := range events {
		reports = append(reports, e, event.Sync())
	}
	return reports
}

func hotkeyFor(machine int) []event.Event {
	return eachFollowedBySync(
		press(keyLeftCtrl), press(keyLeftAlt), press(digit(machine)),
		release(digit(machine)), release(keyLeftAlt), release(keyLeftCtrl),
	)
}

func hotkeyAsTheFocusedReceiverSeesIt() []event.Event {
	return eachFollowedBySync(press(keyLeftCtrl), press(keyLeftAlt), release(keyLeftAlt), release(keyLeftCtrl))
}

type journal struct {
	mutex   sync.Mutex
	entries []string
}

func (j *journal) note(entry string) {
	j.mutex.Lock()
	defer j.mutex.Unlock()
	j.entries = append(j.entries, entry)
}

func (j *journal) read() []string {
	j.mutex.Lock()
	defer j.mutex.Unlock()
	return slices.Clone(j.entries)
}

func (j *journal) has(entry string) bool {
	return slices.Contains(j.read(), entry)
}

func gets(receiver trust.Peer, events ...event.Event) []string {
	entries := make([]string, 0, len(events))
	for _, e := range events {
		entries = append(entries, fmt.Sprintf("%s gets %+v", receiver.Name, e))
	}
	return entries
}

func closed(receiver trust.Peer) string {
	return receiver.Name + " is disconnected"
}

func story(parts ...[]string) []string {
	return slices.Concat(parts...)
}

func then(entries ...string) []string {
	return entries
}

type fakeCapture struct {
	journal       *journal
	events        chan event.Event
	taken         atomic.Bool
	takeFails     atomic.Bool
	giveBackFails atomic.Bool
}

var (
	errTakeFailed     = errors.New("the devices refuse to be grabbed")
	errGiveBackFailed = errors.New("the devices refuse to be released")
	errConnectionLost = errors.New("the connection is lost")
)

func (c *fakeCapture) Events() <-chan event.Event {
	return c.events
}

func (c *fakeCapture) Take() error {
	if c.takeFails.Load() {
		return errTakeFailed
	}
	c.taken.Store(true)
	c.journal.note(tookInput)
	return nil
}

func (c *fakeCapture) GiveBack() error {
	c.taken.Store(false)
	c.journal.note(gaveInputBack)
	if c.giveBackFails.Load() {
		return errGiveBackFailed
	}
	return nil
}

type fakeReceiver struct {
	journal    *journal
	peer       trust.Peer
	lost       chan struct{}
	losing     sync.Once
	shut       chan struct{}
	shutting   sync.Once
	closes     atomic.Int32
	sendFails  atomic.Bool
	sendPanics atomic.Bool
	sendHangs  atomic.Bool
}

func (r *fakeReceiver) Send(events ...event.Event) error {
	if r.sendPanics.Load() {
		panic("the connection exploded")
	}
	if r.sendHangs.Load() {
		<-r.shut
		return errConnectionLost
	}
	if r.sendFails.Load() {
		return errConnectionLost
	}
	for _, entry := range gets(r.peer, events...) {
		r.journal.note(entry)
	}
	return nil
}

func (r *fakeReceiver) Receive() (event.Event, error) {
	<-r.lost
	return event.Event{}, errConnectionLost
}

func (r *fakeReceiver) Close() error {
	r.shutting.Do(func() {
		r.journal.note(closed(r.peer))
		close(r.shut)
	})
	r.closes.Add(1)
	r.loseConnection()
	return nil
}

func (r *fakeReceiver) closedByItsReaderAndThenByTheSharer() bool {
	return r.closes.Load() == 2
}

func (r *fakeReceiver) Peer() trust.Peer {
	return r.peer
}

func (r *fakeReceiver) loseConnection() {
	r.losing.Do(func() { close(r.lost) })
}

type fakeTrusted struct {
	mutex sync.Mutex
	peers []trust.Peer
}

func (f *fakeTrusted) Peers() []trust.Peer {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return slices.Clone(f.peers)
}

func (f *fakeTrusted) trustOnly(peers ...trust.Peer) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.peers = peers
}

type outcome struct {
	err      error
	panicked any
}

type sharing struct {
	t           *testing.T
	journal     *journal
	capture     *fakeCapture
	screen      *fakeScreen
	trusted     *fakeTrusted
	clock       *fakeClock
	desk        atomic.Pointer[layout.Layout]
	speed       Speed
	sharer      *Sharer
	onReceivers func(names []string)
	stop        context.CancelFunc
	finished    chan outcome
}

func startSharing(t *testing.T, peers ...trust.Peer) *sharing {
	t.Helper()
	s := newSharing(t, peers...)
	s.start(s.capture)
	return s
}

func newSharing(t *testing.T, peers ...trust.Peer) *sharing {
	t.Helper()
	written := &journal{}
	return &sharing{
		t:        t,
		journal:  written,
		capture:  &fakeCapture{journal: written, events: make(chan event.Event)},
		trusted:  &fakeTrusted{peers: peers},
		clock:    &fakeClock{},
		speed:    Speed{X: 100, Y: 100},
		finished: make(chan outcome, 1),
	}
}

func (s *sharing) start(capture Capture) {
	s.t.Helper()
	detector, err := hotkey.New(hotkey.DefaultModifiers)
	if err != nil {
		s.t.Fatalf("hotkey.New: %v", err)
	}
	s.sharer = New(capture, s.trusted, detector, EdgeSwitching{
		Name:   thisMachine,
		Layout: s.desk.Load,
		Speed:  s.speed,
		Watch:  edge.DefaultSettings(),
	})
	s.sharer.trustCheckInterval = testTrustCheck
	s.sharer.now = s.clock.now
	if s.onReceivers != nil {
		s.sharer.OnReceivers(s.onReceivers)
	}
	ctx, stop := context.WithCancel(context.Background())
	s.stop = stop
	go func() {
		var result outcome
		defer func() {
			result.panicked = recover()
			s.finished <- result
		}()
		result.err = s.sharer.Run(ctx)
	}()
	s.t.Cleanup(stop)
}

func (s *sharing) connect(peer trust.Peer) *fakeReceiver {
	receiver := &fakeReceiver{journal: s.journal, peer: peer, lost: make(chan struct{}), shut: make(chan struct{})}
	s.sharer.Connect(receiver)
	return receiver
}

func (s *sharing) captures(events ...event.Event) {
	s.t.Helper()
	s.capturesWithoutWaitingForTheLast(append(slices.Clone(events), ignored)...)
}

func (s *sharing) capturesWithoutWaitingForTheLast(events ...event.Event) {
	s.t.Helper()
	for _, e := range events {
		select {
		case s.capture.events <- e:
		case <-s.sharer.stopped:
			return
		case <-time.After(testPatience):
			s.t.Fatalf("the sharer did not read %+v", e)
		}
	}
}

func (s *sharing) focusOn(machine int) {
	s.t.Helper()
	s.captures(hotkeyFor(machine)...)
}

func (s *sharing) outcome() outcome {
	s.t.Helper()
	select {
	case result := <-s.finished:
		return result
	case <-time.After(testPatience):
		s.t.Fatal("the sharer did not stop")
		return outcome{}
	}
}

func (s *sharing) eventually(what string, happened func() bool) {
	s.t.Helper()
	for deadline := time.Now().Add(testPatience); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		if happened() {
			return
		}
	}
	s.t.Fatalf("%s never happened, the journal is:\n%s", what, strings.Join(s.journal.read(), "\n"))
}

func (s *sharing) expectJournal(want []string) {
	s.t.Helper()
	if got := s.journal.read(); !slices.Equal(got, want) {
		s.t.Errorf("the journal is:\n%s\n\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func (s *sharing) expectInputAtThisMachine() {
	s.t.Helper()
	if s.capture.taken.Load() {
		s.t.Error("the input is still taken away from this machine")
	}
}

func TestHotkeyForAConnectedReceiverTakesTheInputAwayAndForwardsWhatIsCaptured(t *testing.T) {
	s := startSharing(t, desk)
	s.connect(desk)
	typing := eachFollowedBySync(press(event.KeyA), release(event.KeyA))

	s.captures(typing...)
	s.focusOn(2)
	s.captures(typing...)

	s.expectJournal(story(
		then(tookInput),
		gets(desk, event.Enter()),
		gets(desk, typing...),
	))
}

func TestHotkeyForTheSharerSendsLeaveAndGivesTheInputBack(t *testing.T) {
	s := startSharing(t, desk)
	s.connect(desk)
	s.focusOn(2)

	s.focusOn(1)
	s.captures(eachFollowedBySync(press(event.KeyA), release(event.KeyA))...)

	s.expectJournal(story(
		then(tookInput),
		gets(desk, event.Enter()),
		gets(desk, hotkeyAsTheFocusedReceiverSeesIt()...),
		gets(desk, event.Leave()),
		then(gaveInputBack),
	))
}

func TestHotkeyForTheSharerWhileItHasFocusChangesNothing(t *testing.T) {
	s := startSharing(t, desk)
	s.connect(desk)

	s.focusOn(1)

	s.expectJournal(nil)
}

func TestHotkeyThatCannotBeFollowedChangesNothing(t *testing.T) {
	tests := []struct {
		name    string
		machine int
	}{
		{"a trusted receiver that is not connected", 3},
		{"a number no machine has", 4},
		{"the last number of the hotkey", 9},
	}
	for _, test := range tests {
		t.Run(test.name+", with focus on the sharer", func(t *testing.T) {
			s := startSharing(t, desk, laptop)
			s.connect(desk)

			s.focusOn(test.machine)
			s.captures(eachFollowedBySync(press(event.KeyA), release(event.KeyA))...)

			s.expectJournal(nil)
		})
		t.Run(test.name+", with focus on a receiver", func(t *testing.T) {
			s := startSharing(t, desk, laptop)
			s.connect(desk)
			s.focusOn(2)
			typing := eachFollowedBySync(press(event.KeyA), release(event.KeyA))

			s.focusOn(test.machine)
			s.captures(typing...)

			s.expectJournal(story(
				then(tookInput),
				gets(desk, event.Enter()),
				gets(desk, hotkeyAsTheFocusedReceiverSeesIt()...),
				gets(desk, typing...),
			))
		})
	}
}

func TestHotkeyForTheReceiverThatAlreadyHasFocusChangesNothing(t *testing.T) {
	s := startSharing(t, desk)
	s.connect(desk)
	s.focusOn(2)

	s.focusOn(2)

	s.expectJournal(story(
		then(tookInput),
		gets(desk, event.Enter()),
		gets(desk, hotkeyAsTheFocusedReceiverSeesIt()...),
	))
}

func TestSwitchingFromOneReceiverToAnotherLeavesTheFirstBeforeEnteringTheSecond(t *testing.T) {
	s := startSharing(t, desk, laptop)
	s.connect(desk)
	s.connect(laptop)
	s.focusOn(2)
	typing := eachFollowedBySync(press(event.KeyA), release(event.KeyA))

	s.focusOn(3)
	s.captures(typing...)

	s.expectJournal(story(
		then(tookInput),
		gets(desk, event.Enter()),
		gets(desk, hotkeyAsTheFocusedReceiverSeesIt()...),
		gets(desk, event.Leave()),
		gets(laptop, event.Enter()),
		gets(laptop, typing...),
	))
}

func TestReceiversAreNumberedInTheOrderOfTheTrustStoreNotOfConnection(t *testing.T) {
	s := startSharing(t, desk, laptop, tablet)
	s.connect(tablet)
	s.connect(laptop)
	s.connect(desk)

	s.focusOn(4)

	s.expectJournal(story(
		then(tookInput),
		gets(tablet, event.Enter()),
	))
}

func TestTheDigitOfTheHotkeyIsNeverForwarded(t *testing.T) {
	s := startSharing(t, desk)
	s.connect(desk)
	s.focusOn(2)

	s.captures(eachFollowedBySync(
		press(keyLeftCtrl), press(keyLeftAlt),
		press(digit(2)), repeat(digit(2)), repeat(digit(2)), release(digit(2)),
		press(event.KeyA), release(event.KeyA),
		release(keyLeftAlt), release(keyLeftCtrl),
	)...)

	s.expectJournal(story(
		then(tookInput),
		gets(desk, event.Enter()),
		gets(desk, eachFollowedBySync(
			press(keyLeftCtrl), press(keyLeftAlt),
			press(event.KeyA), release(event.KeyA),
			release(keyLeftAlt), release(keyLeftCtrl),
		)...),
	))
}

func TestWhatIsForwardedWhileAReceiverHasFocus(t *testing.T) {
	tests := []struct {
		name     string
		captured []event.Event
		want     []event.Event
	}{
		{
			name:     "keys with their sync",
			captured: []event.Event{press(event.KeyA), event.Sync(), release(event.KeyA), event.Sync()},
			want:     []event.Event{press(event.KeyA), event.Sync(), release(event.KeyA), event.Sync()},
		},
		{
			name:     "mouse movement, wheels and buttons",
			captured: []event.Event{event.Relative(event.RelX, 7), event.Relative(event.RelY, -3), event.Sync(), event.Relative(event.RelWheel, -1), event.Relative(event.RelHWheel, 2), event.Sync(), press(event.ButtonLeft), event.Sync(), release(event.ButtonLeft), event.Sync()},
			want:     []event.Event{event.Relative(event.RelX, 7), event.Relative(event.RelY, -3), event.Sync(), event.Relative(event.RelWheel, -1), event.Relative(event.RelHWheel, 2), event.Sync(), press(event.ButtonLeft), event.Sync(), release(event.ButtonLeft), event.Sync()},
		},
		{
			name:     "key repeats are dropped, with the sync that follows them",
			captured: []event.Event{press(event.KeyA), event.Sync(), repeat(event.KeyA), event.Sync(), repeat(event.KeyA), event.Sync(), release(event.KeyA), event.Sync()},
			want:     []event.Event{press(event.KeyA), event.Sync(), release(event.KeyA), event.Sync()},
		},
		{
			name:     "events that are not keys, movement or sync are dropped",
			captured: []event.Event{ignored, press(event.KeyA), event.Sync(), ignored, release(event.KeyA), event.Sync(), ignored, event.Sync()},
			want:     []event.Event{press(event.KeyA), event.Sync(), release(event.KeyA), event.Sync()},
		},
		{
			name:     "a sync that is not the end of a report is dropped",
			captured: []event.Event{{Type: event.TypeSync, Code: syncDropped}, press(event.KeyA), event.Sync(), release(event.KeyA), event.Sync()},
			want:     []event.Event{press(event.KeyA), event.Sync(), release(event.KeyA), event.Sync()},
		},
		{
			name:     "the sync the kernel sends with a value when a device goes away ends the report like any other",
			captured: []event.Event{press(event.KeyA), event.Sync(), release(event.KeyA), {Type: event.TypeSync, Code: event.SyncReport, Value: 1}},
			want:     []event.Event{press(event.KeyA), event.Sync(), release(event.KeyA), event.Sync()},
		},
		{
			name:     "reports of two devices that interleave end with one sync",
			captured: []event.Event{event.Relative(event.RelX, 1), press(event.KeyA), event.Sync(), event.Sync(), release(event.KeyA), event.Sync()},
			want:     []event.Event{event.Relative(event.RelX, 1), press(event.KeyA), event.Sync(), release(event.KeyA), event.Sync()},
		},
		{
			name:     "control events found in the captured input are dropped",
			captured: []event.Event{event.Leave(), event.Enter(), event.Ping(), press(event.KeyA), event.Sync(), release(event.KeyA), event.Sync()},
			want:     []event.Event{press(event.KeyA), event.Sync(), release(event.KeyA), event.Sync()},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := startSharing(t, desk)
			s.connect(desk)
			s.focusOn(2)

			s.captures(test.captured...)

			s.expectJournal(story(
				then(tookInput),
				gets(desk, event.Enter()),
				gets(desk, test.want...),
			))
		})
	}
}

func TestNothingIsForwardedWhileTheSharerHasFocus(t *testing.T) {
	s := startSharing(t, desk)
	s.connect(desk)

	s.captures(eachFollowedBySync(press(event.KeyA), event.Relative(event.RelX, 5), release(event.KeyA))...)

	s.expectJournal(nil)
}

func TestStoppingReleasesWhatIsHeldOnTheReceiverBeforeLeaveAndGivesTheInputBack(t *testing.T) {
	s := startSharing(t, desk)
	s.connect(desk)
	s.focusOn(2)
	held := eachFollowedBySync(press(keyLeftShift), press(event.KeyA), press(keyB), release(keyB), press(event.ButtonLeft))
	s.captures(held...)

	s.stop()
	result := s.outcome()

	if result.err != nil || result.panicked != nil {
		t.Errorf("the sharer stopped with %+v, want a clean stop", result)
	}
	s.expectJournal(story(
		then(tookInput),
		gets(desk, event.Enter()),
		gets(desk, held...),
		gets(desk, release(event.KeyA), release(keyLeftShift), release(event.ButtonLeft), event.Sync(), event.Leave()),
		then(gaveInputBack, closed(desk)),
	))
	s.expectInputAtThisMachine()
}

func TestLeavingSendsTheReleaseThatHasNoSyncYetWithASyncBeforeLeave(t *testing.T) {
	s := startSharing(t, desk)
	s.connect(desk)
	s.focusOn(2)

	s.captures(press(event.KeyA), event.Sync(), release(event.KeyA))
	s.stop()
	s.outcome()

	s.expectJournal(story(
		then(tookInput),
		gets(desk, event.Enter()),
		gets(desk, press(event.KeyA), event.Sync()),
		gets(desk, release(event.KeyA), event.Sync(), event.Leave()),
		then(gaveInputBack, closed(desk)),
	))
}

func TestReceiverLostWhileItHasFocusGivesTheInputBackAtOnce(t *testing.T) {
	s := startSharing(t, desk)
	receiver := s.connect(desk)
	s.focusOn(2)
	s.captures(press(event.KeyA), event.Sync())

	receiver.loseConnection()
	s.eventually("giving the input back", func() bool { return s.journal.has(gaveInputBack) })
	s.captures(release(event.KeyA), event.Sync())
	s.focusOn(2)

	s.expectJournal(story(
		then(tookInput),
		gets(desk, event.Enter()),
		gets(desk, press(event.KeyA), event.Sync()),
		then(closed(desk), gaveInputBack),
	))
	s.expectInputAtThisMachine()
}

func TestReceiverLostWithoutFocusLeavesTheFocusWhereItIs(t *testing.T) {
	s := startSharing(t, desk, laptop)
	s.connect(desk)
	other := s.connect(laptop)
	s.focusOn(2)
	typing := eachFollowedBySync(press(event.KeyA), release(event.KeyA))

	other.loseConnection()
	s.eventually("dropping the laptop", other.closedByItsReaderAndThenByTheSharer)
	s.captures(typing...)
	s.focusOn(3)

	s.expectJournal(story(
		then(tookInput),
		gets(desk, event.Enter()),
		then(closed(laptop)),
		gets(desk, typing...),
		gets(desk, hotkeyAsTheFocusedReceiverSeesIt()...),
	))
}

func TestReceiverThatCannotBeSentToIsLostAndTheInputIsGivenBack(t *testing.T) {
	s := startSharing(t, desk)
	receiver := s.connect(desk)
	s.focusOn(2)

	receiver.sendFails.Store(true)
	s.captures(press(event.KeyA), event.Sync())

	s.expectJournal(story(
		then(tookInput),
		gets(desk, event.Enter()),
		then(gaveInputBack, closed(desk)),
	))
	s.expectInputAtThisMachine()
}

func TestReceiverLostWhileASendToItHangsGivesTheInputBackAtOnce(t *testing.T) {
	s := startSharing(t, desk)
	receiver := s.connect(desk)
	s.focusOn(2)
	receiver.sendHangs.Store(true)
	s.capturesWithoutWaitingForTheLast(press(event.KeyA), event.Sync())

	receiver.loseConnection()
	s.eventually("giving the input back", func() bool { return s.journal.has(gaveInputBack) })

	s.expectJournal(story(
		then(tookInput),
		gets(desk, event.Enter()),
		then(closed(desk), gaveInputBack),
	))
	s.expectInputAtThisMachine()
}

func TestReceiverThatCannotBeEnteredIsLostAndTheInputIsGivenBack(t *testing.T) {
	s := startSharing(t, desk)
	receiver := s.connect(desk)
	receiver.sendFails.Store(true)

	s.focusOn(2)

	s.expectJournal(then(tookInput, gaveInputBack, closed(desk)))
	s.expectInputAtThisMachine()
}

func TestSwitchingAwayFromAReceiverThatCannotBeSentToStillEntersTheOther(t *testing.T) {
	s := startSharing(t, desk, laptop)
	first := s.connect(desk)
	s.connect(laptop)
	s.focusOn(2)
	untilTheLastRelease := eachFollowedBySync(press(keyLeftCtrl), press(keyLeftAlt), press(digit(3)), release(digit(3)), release(keyLeftAlt))
	typing := eachFollowedBySync(press(event.KeyA), release(event.KeyA))

	s.captures(untilTheLastRelease...)
	first.sendFails.Store(true)
	s.captures(release(keyLeftCtrl), event.Sync())
	s.captures(typing...)

	s.expectJournal(story(
		then(tookInput),
		gets(desk, event.Enter()),
		gets(desk, eachFollowedBySync(press(keyLeftCtrl), press(keyLeftAlt), release(keyLeftAlt))...),
		then(closed(desk)),
		gets(laptop, event.Enter()),
		gets(laptop, typing...),
	))
}

func TestReceiverThatReconnectsReplacesItsOldConnection(t *testing.T) {
	s := startSharing(t, desk)
	old := s.connect(desk)
	typing := eachFollowedBySync(press(event.KeyA), release(event.KeyA))

	s.connect(desk)
	s.sharer.losses <- old
	s.focusOn(2)
	s.captures(typing...)

	s.expectJournal(story(
		then(closed(desk), tookInput),
		gets(desk, event.Enter()),
		gets(desk, typing...),
	))
	select {
	case <-old.lost:
	default:
		t.Error("the old connection is still open")
	}
}

func TestReceiverThatReconnectsWhileItHasFocusGivesTheInputBackUntilTheHotkeyIsPressedAgain(t *testing.T) {
	s := startSharing(t, desk)
	s.connect(desk)
	s.focusOn(2)
	s.captures(press(event.KeyA), event.Sync())
	typing := eachFollowedBySync(press(keyB), release(keyB))

	s.connect(desk)
	s.captures(release(event.KeyA), event.Sync())
	s.focusOn(2)
	s.captures(typing...)

	s.expectJournal(story(
		then(tookInput),
		gets(desk, event.Enter()),
		gets(desk, press(event.KeyA), event.Sync()),
		then(gaveInputBack, closed(desk), tookInput),
		gets(desk, event.Enter()),
		gets(desk, typing...),
	))
}

func TestKeysHeldOnAReceiverThatWasLostAreNotReleasedOnItsNextVisit(t *testing.T) {
	s := startSharing(t, desk)
	lost := s.connect(desk)
	s.focusOn(2)
	s.captures(press(event.KeyA), event.Sync())
	lost.loseConnection()
	s.eventually("giving the input back", func() bool { return s.journal.has(gaveInputBack) })
	s.captures(release(event.KeyA), event.Sync())

	s.connect(desk)
	s.focusOn(2)
	s.focusOn(1)

	s.expectJournal(story(
		then(tookInput),
		gets(desk, event.Enter()),
		gets(desk, press(event.KeyA), event.Sync()),
		then(closed(desk), gaveInputBack, tookInput),
		gets(desk, event.Enter()),
		gets(desk, hotkeyAsTheFocusedReceiverSeesIt()...),
		gets(desk, event.Leave()),
		then(gaveInputBack),
	))
}

func TestMachineRemovedFromTheTrustStoreIsDisconnectedAndLosesItsNumber(t *testing.T) {
	s := startSharing(t, desk, laptop)
	s.connect(desk)
	s.connect(laptop)
	s.focusOn(2)

	s.trusted.trustOnly(laptop)
	s.eventually("giving the input back", func() bool { return s.journal.has(gaveInputBack) })
	s.focusOn(2)

	s.expectJournal(story(
		then(tookInput),
		gets(desk, event.Enter()),
		then(gaveInputBack, closed(desk), tookInput),
		gets(laptop, event.Enter()),
	))
}

func TestMachineAddedToTheTrustStoreGetsItsNumberWithoutARestart(t *testing.T) {
	s := startSharing(t, desk)
	s.connect(desk)

	s.trusted.trustOnly(desk, laptop)
	s.connect(laptop)
	s.focusOn(3)

	s.expectJournal(story(
		then(tookInput),
		gets(laptop, event.Enter()),
	))
}

func TestConnectionOfAMachineThatIsNotTrustedIsClosed(t *testing.T) {
	s := startSharing(t, desk)

	s.connect(laptop)

	s.eventually("disconnecting the laptop", func() bool { return s.journal.has(closed(laptop)) })
}

func TestInputIsGivenBackHoweverTheSharerStops(t *testing.T) {
	tests := []struct {
		name         string
		stop         func(s *sharing, receiver *fakeReceiver)
		wantErr      error
		wantPanic    bool
		wantLastSeen []string
	}{
		{
			name:         "it is asked to stop",
			stop:         func(s *sharing, _ *fakeReceiver) { s.stop() },
			wantLastSeen: story(gets(desk, event.Leave()), then(gaveInputBack, closed(desk))),
		},
		{
			name:         "the capture stops",
			stop:         func(s *sharing, _ *fakeReceiver) { close(s.capture.events) },
			wantErr:      errCaptureStopped,
			wantLastSeen: then(gaveInputBack, closed(desk)),
		},
		{
			name: "the input cannot be given back when the hotkey asks for it",
			stop: func(s *sharing, _ *fakeReceiver) {
				s.capture.giveBackFails.Store(true)
				s.focusOn(1)
			},
			wantErr:      errGiveBackFailed,
			wantLastSeen: story(gets(desk, event.Leave()), then(gaveInputBack, gaveInputBack, closed(desk))),
		},
		{
			name: "the focus logic panics",
			stop: func(s *sharing, receiver *fakeReceiver) {
				receiver.sendPanics.Store(true)
				s.captures(press(event.KeyA), event.Sync())
			},
			wantPanic:    true,
			wantLastSeen: then(gaveInputBack, closed(desk)),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := startSharing(t, desk)
			receiver := s.connect(desk)
			s.focusOn(2)

			test.stop(s, receiver)
			result := s.outcome()

			if !errors.Is(result.err, test.wantErr) || (result.panicked != nil) != test.wantPanic {
				t.Errorf("the sharer stopped with %+v, want error %v and panic %v", result, test.wantErr, test.wantPanic)
			}
			seen := s.journal.read()
			if last := seen[max(0, len(seen)-len(test.wantLastSeen)):]; !slices.Equal(last, test.wantLastSeen) {
				t.Errorf("the journal ends with:\n%s\n\nwant:\n%s", strings.Join(last, "\n"), strings.Join(test.wantLastSeen, "\n"))
			}
			s.expectInputAtThisMachine()
		})
	}
}

func TestSharerStopsWithoutFocusOnAReceiverWhenTheInputCannotBeTakenAway(t *testing.T) {
	s := startSharing(t, desk)
	s.connect(desk)
	s.capture.takeFails.Store(true)

	s.focusOn(2)
	result := s.outcome()

	if !errors.Is(result.err, errTakeFailed) {
		t.Errorf("the sharer stopped with %+v, want %v", result, errTakeFailed)
	}
	s.expectJournal(then(gaveInputBack, closed(desk)))
}

func TestConnectionThatArrivesAfterTheSharerStoppedIsClosed(t *testing.T) {
	s := startSharing(t, desk)
	s.stop()
	s.outcome()

	late := s.connect(desk)

	select {
	case <-late.lost:
	case <-time.After(testPatience):
		t.Error("the late connection was left open")
	}
}

func TestEveryChangeOfTheConnectedMachinesIsReportedWithTheirNames(t *testing.T) {
	reports := &journal{}
	s := newSharing(t, desk, laptop)
	s.onReceivers = func(names []string) { reports.note(fmt.Sprint(names)) }
	s.start(s.capture)
	s.eventually("the empty list is reported at the start", func() bool { return slices.Equal(reports.read(), []string{"[]"}) })
	lost := s.connect(laptop)
	s.connect(desk)
	s.eventually("both arrivals are reported", func() bool {
		return slices.Equal(reports.read(), []string{"[]", "[laptop]", "[desk laptop]"})
	})
	lost.loseConnection()
	s.eventually("the loss is reported", func() bool {
		return slices.Equal(reports.read(), []string{"[]", "[laptop]", "[desk laptop]", "[desk]"})
	})
	s.captures(press(keyB), release(keyB))
	if got := reports.read(); len(got) != 4 {
		t.Errorf("reports = %v, want no report for input that changes no connection", got)
	}
}
