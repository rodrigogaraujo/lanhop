package follow

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/features"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
	"github.com/rodrigogaraujo/lanhop/internal/update"
)

const patience = 2 * time.Second

var (
	v011 = update.Version{Major: 0, Minor: 1, Patch: 1}
	v012 = update.Version{Major: 0, Minor: 1, Patch: 2}
	v013 = update.Version{Major: 0, Minor: 1, Patch: 3}
	v014 = update.Version{Major: 0, Minor: 1, Patch: 4}
)

type wire struct {
	name  string
	mutex sync.Mutex
	sent  []event.Event
}

func (w *wire) Send(events ...event.Event) error {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	w.sent = append(w.sent, events...)
	return nil
}

func (w *wire) Receive() (event.Event, error) {
	return event.Event{}, errors.New("not read in these tests")
}
func (w *wire) Close() error     { return nil }
func (w *wire) Peer() trust.Peer { return trust.Peer{Name: w.name} }

func (w *wire) frames(code uint16) []event.Event {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return slices.DeleteFunc(slices.Clone(w.sent), func(sent event.Event) bool { return !sent.IsControl() || sent.Code != code })
}

func eventually(t *testing.T, what string, holds func() bool) {
	t.Helper()
	for deadline := time.Now().Add(patience); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if holds() {
			return
		}
	}
	t.Fatalf("%s never happened", what)
}

func never(t *testing.T, what string, happened func() bool) {
	t.Helper()
	for deadline := time.Now().Add(120 * time.Millisecond); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if happened() {
			t.Fatalf("%s happened", what)
		}
	}
}

func followsFrame(on bool) event.Event {
	if on {
		return event.Features(1<<features.Follows | 1)
	}
	return event.Features(1)
}

func seatOf(t *testing.T, central *Central, name string) Seat {
	t.Helper()
	for _, seat := range central.Seats() {
		if seat.Name == name {
			return seat
		}
	}
	t.Fatalf("no seat named %s in %+v", name, central.Seats())
	return Seat{}
}

func TestTheCentralSendsUpdateOnceToAnOlderMachineThatFollows(t *testing.T) {
	central := NewCentral(CentralConfig{Running: v013, From: v012})
	conn := &wire{name: "office-mac"}
	heard := central.Connected(conn)
	heard(followsFrame(true))
	heard(event.Version(v012.Value()))
	heard(event.Version(v012.Value()))

	updates := conn.frames(event.ControlUpdate)
	if len(updates) != 1 || updates[0].Value != v013.Value() {
		t.Fatalf("the central sent %+v, want one Update to v0.1.3", updates)
	}
	if seat := seatOf(t, central, "office-mac"); seat.Mark != Follows || seat.Version != v012 {
		t.Errorf("seat %+v, want office-mac on v0.1.2, following", seat)
	}
}

func TestTheCentralLeavesNewerMachinesAndMachinesBelowItsFromAlone(t *testing.T) {
	central := NewCentral(CentralConfig{Running: v013, From: v012})
	newer, below := &wire{name: "newer"}, &wire{name: "below"}
	toNewer, toBelow := central.Connected(newer), central.Connected(below)
	toNewer(followsFrame(true))
	toNewer(event.Version(v014.Value()))
	toBelow(followsFrame(true))
	toBelow(event.Version(v011.Value()))

	if got := append(newer.frames(event.ControlUpdate), below.frames(event.ControlUpdate)...); len(got) != 0 {
		t.Errorf("the central sent %+v, want nothing", got)
	}
	if seat := seatOf(t, central, "below"); seat.Mark != NeedsTheInstaller {
		t.Errorf("a machine below the central's from is marked %q, want %q", seat.Mark, NeedsTheInstaller)
	}
	if seat := seatOf(t, central, "newer"); seat.Mark != Follows {
		t.Errorf("a newer machine is marked %q, want %q", seat.Mark, Follows)
	}
}

func TestAMachineWithUpdatesOffGetsUpdateOnlyWhenItTurnsThemOn(t *testing.T) {
	central := NewCentral(CentralConfig{Running: v013})
	conn := &wire{name: "desk"}
	heard := central.Connected(conn)
	heard(followsFrame(false))
	heard(event.Version(v012.Value()))
	if got := conn.frames(event.ControlUpdate); len(got) != 0 {
		t.Fatalf("the central sent %+v to a machine with updates off", got)
	}
	if seat := seatOf(t, central, "desk"); seat.Mark != DoesNotFollow {
		t.Errorf("seat marked %q, want %q", seat.Mark, DoesNotFollow)
	}
	heard(followsFrame(true))
	if got := conn.frames(event.ControlUpdate); len(got) != 1 {
		t.Fatalf("after updates on, the central sent %+v, want one Update", got)
	}
	heard(followsFrame(false))
	heard(followsFrame(true))
	if got := conn.frames(event.ControlUpdate); len(got) != 2 {
		t.Errorf("after updates off and on again, the central sent %d Updates, want 2", len(got))
	}
}

func TestAMachineThatNeverSaysItsVersionNeedsTheInstallerAndGetsNothing(t *testing.T) {
	marks := make(chan []Seat, 10)
	central := NewCentral(CentralConfig{Running: v013, VersionPatience: 30 * time.Millisecond, OnChange: func(seats []Seat) { marks <- seats }})
	conn := &wire{name: "old"}
	central.Connected(conn)
	eventually(t, "the mark needs the installer", func() bool { return seatOf(t, central, "old").Mark == NeedsTheInstaller })
	if got := conn.frames(event.ControlUpdate); len(got) != 0 {
		t.Errorf("the central sent %+v to a machine that knows nothing of updates", got)
	}
	central.Lost(conn)
	if seats := central.Seats(); len(seats) != 0 {
		t.Errorf("seats %+v after the machine was lost, want none", seats)
	}
}

func TestACentralThatIsNotAReleaseSendsNothing(t *testing.T) {
	central := NewCentral(CentralConfig{})
	conn := &wire{name: "desk"}
	heard := central.Connected(conn)
	heard(followsFrame(true))
	heard(event.Version(v012.Value()))
	if got := conn.frames(event.ControlUpdate); len(got) != 0 {
		t.Errorf("a central that is not a release sent %+v", got)
	}
}

type fakePrepared struct {
	updater *fakeUpdater
	version update.Version
}

func (p fakePrepared) Install() error {
	p.updater.installed.Store(int32(p.version.Value()))
	return nil
}

func (p fakePrepared) Discard() { p.updater.discarded.Add(1) }

type fakeUpdater struct {
	prepared  atomic.Int32
	discarded atomic.Int32
	installed atomic.Int32
	failing   atomic.Bool
	gate      chan struct{}
	gateFrom  int32
}

func (u *fakeUpdater) Prepare(_ context.Context, want update.Version) (Prepared, error) {
	if count := u.prepared.Add(1); u.gate != nil && count >= u.gateFrom {
		<-u.gate
	}
	if u.failing.Load() {
		return nil, errors.New("the download was refused")
	}
	return fakePrepared{updater: u, version: want}, nil
}

type sideUnderTest struct {
	side      *Side
	conn      *wire
	heard     func(event.Event)
	updater   *fakeUpdater
	restarted atomic.Bool
	following atomic.Bool
	busy      atomic.Bool
	set       *features.Set
}

func newSideUnderTest(t *testing.T, running update.Version) *sideUnderTest {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	under := &sideUnderTest{conn: &wire{name: "central"}, updater: &fakeUpdater{}, set: new(features.Set)}
	under.following.Store(true)
	under.side = NewSide(ctx, SideConfig{
		Running:         running,
		Following:       under.following.Load,
		Features:        under.set,
		Updater:         under.updater,
		Restart:         func() { under.restarted.Store(true) },
		Busy:            under.busy.Load,
		QuietAfterLeave: 400 * time.Millisecond,
	})
	under.heard = under.side.Connected(under.conn)
	return under
}

func TestASideMachineThatFollowsSaysSoAndSaysItsVersion(t *testing.T) {
	under := newSideUnderTest(t, v012)
	features := under.conn.frames(event.ControlFeatures)
	versions := under.conn.frames(event.ControlVersion)
	if len(features) != 1 || features[0].Value&(1<<8) == 0 {
		t.Errorf("the side machine sent Features %+v, want bit 8 set", features)
	}
	if len(versions) != 1 || versions[0].Value != v012.Value() {
		t.Errorf("the side machine sent Version %+v, want v0.1.2", versions)
	}
}

func TestASideMachineThatIsNotAReleaseNeverFollows(t *testing.T) {
	under := newSideUnderTest(t, update.Version{})
	if got := under.conn.frames(event.ControlFeatures); len(got) != 1 || got[0].Value&(1<<8) != 0 {
		t.Errorf("a build that is not a release sent Features %+v, want bit 8 clear", got)
	}
	if got := under.conn.frames(event.ControlVersion); len(got) != 1 || got[0].Value != 0 {
		t.Errorf("a build that is not a release sent Version %+v, want 0", got)
	}
	under.heard(event.Update(v013.Value()))
	never(t, "a download", func() bool { return under.updater.prepared.Load() > 0 })
}

func TestAnUpdateWithoutFocusInstallsAndRestarts(t *testing.T) {
	under := newSideUnderTest(t, v012)
	under.heard(event.Update(v013.Value()))
	eventually(t, "the restart", under.restarted.Load)
	if got := under.updater.installed.Load(); got != v013.Value() {
		t.Errorf("installed %d, want v0.1.3", got)
	}
}

func TestAnUpdateWaitsUntilFocusLeftAndChecksAgainBeforeInstalling(t *testing.T) {
	under := newSideUnderTest(t, v012)
	under.side.FocusArrives()
	under.heard(event.Update(v013.Value()))
	eventually(t, "the download", func() bool { return under.updater.prepared.Load() == 1 })
	never(t, "a restart while focused", under.restarted.Load)

	under.side.FocusLeaves()
	never(t, "a restart right after the leave", under.restarted.Load)
	eventually(t, "the restart after a quiet time", under.restarted.Load)
	if got := under.updater.prepared.Load(); got != 2 {
		t.Errorf("Prepare ran %d times, want twice: once at the Update and once again before installing", got)
	}
	if got := under.updater.discarded.Load(); got != 1 {
		t.Errorf("the first copy was discarded %d times, want once", got)
	}
}

func TestAnUpdateWaitsWhileTheMachineIsBusy(t *testing.T) {
	under := newSideUnderTest(t, v012)
	under.busy.Store(true)
	under.heard(event.Update(v013.Value()))
	never(t, "a restart while busy", under.restarted.Load)
	under.busy.Store(false)
	eventually(t, "the restart once the machine is not busy", under.restarted.Load)
}

func TestASideMachineNeverMovesDownOrRepeatsTheSameRelease(t *testing.T) {
	under := newSideUnderTest(t, v013)
	under.heard(event.Update(v012.Value()))
	under.heard(event.Update(v013.Value()))
	never(t, "a download", func() bool { return under.updater.prepared.Load() > 0 })
}

func idle(side *Side) bool {
	side.mutex.Lock()
	defer side.mutex.Unlock()
	return !side.busy
}

func TestTurningUpdatesOffSendsFeaturesAgainAndIgnoresUpdate(t *testing.T) {
	under := newSideUnderTest(t, v012)
	under.set.Turn(features.Clipboard, true)
	under.following.Store(false)
	under.side.FocusArrives()
	sent := under.conn.frames(event.ControlFeatures)
	if len(sent) != 2 || sent[1].Value&(1<<8) != 0 {
		t.Fatalf("Features sent %+v, want a second one with bit 8 clear", sent)
	}
	if sent[1].Value&1 != 1 {
		t.Errorf("the Features sent again cleared bit 0 of the clipboard, want the other bits kept")
	}
	under.heard(event.Update(v013.Value()))
	never(t, "a download with updates off", func() bool { return under.updater.prepared.Load() > 0 })
}

func TestAFailedDownloadIsTriedAgainAtTheNextUpdate(t *testing.T) {
	under := newSideUnderTest(t, v012)
	under.updater.failing.Store(true)
	under.heard(event.Update(v013.Value()))
	eventually(t, "the failed download", func() bool { return under.updater.prepared.Load() == 1 && idle(under.side) })
	never(t, "a restart after a failed download", under.restarted.Load)
	under.updater.failing.Store(false)
	under.heard(event.Update(v013.Value()))
	eventually(t, "the restart after the second Update", under.restarted.Load)
}

func TestFocusThatComesBackDuringTheSecondDownloadDelaysTheRestart(t *testing.T) {
	under := newSideUnderTest(t, v012)
	under.updater.gate = make(chan struct{})
	under.updater.gateFrom = 2
	under.side.FocusArrives()
	under.heard(event.Update(v013.Value()))
	eventually(t, "the first download", func() bool { return under.updater.prepared.Load() == 1 })

	under.side.FocusLeaves()
	eventually(t, "the second download, after the quiet time", func() bool { return under.updater.prepared.Load() == 2 })
	under.side.FocusArrives()
	under.updater.gate <- struct{}{}
	never(t, "a restart while the person is back on the machine", under.restarted.Load)

	close(under.updater.gate)
	under.side.FocusLeaves()
	eventually(t, "the restart once the machine is quiet again", under.restarted.Load)
}

func TestTurningUpdatesOffDropsAnUpdateThatWaits(t *testing.T) {
	under := newSideUnderTest(t, v012)
	under.side.FocusArrives()
	under.heard(event.Update(v013.Value()))
	eventually(t, "the download", func() bool { return under.updater.prepared.Load() == 1 })
	under.following.Store(false)
	under.side.FocusLeaves()
	eventually(t, "the waiting copy to be dropped", func() bool { return under.updater.discarded.Load() == 1 })
	never(t, "an install after updates were turned off", func() bool { return under.updater.installed.Load() != 0 || under.restarted.Load() })
}

func TestALostConnectionCountsAsALeave(t *testing.T) {
	under := newSideUnderTest(t, v012)
	under.side.FocusArrives()
	under.heard(event.Update(v013.Value()))
	eventually(t, "the download", func() bool { return under.updater.prepared.Load() == 1 })
	under.side.Lost()
	never(t, "a restart right after the connection was lost while focused", under.restarted.Load)
	eventually(t, "the restart after the quiet time", under.restarted.Load)
}

type connThatSendsTheLatest struct {
	wire
	announced []int32
}

func (c *connThatSendsTheLatest) SendFeatures(announced *features.Set) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.announced = append(c.announced, announced.Value())
	return nil
}

func TestTheSideMachineAnnouncesThroughTheConnectionWhenItCan(t *testing.T) {
	set := new(features.Set)
	set.Turn(features.Clipboard, true)
	side := NewSide(context.Background(), SideConfig{
		Running:   v012,
		Following: func() bool { return true },
		Features:  set,
		Updater:   &fakeUpdater{},
		Restart:   func() {},
	})
	conn := &connThatSendsTheLatest{}
	side.Connected(conn)
	if got := conn.frames(event.ControlFeatures); len(got) != 0 {
		t.Errorf("the side machine built Features by hand: %+v", got)
	}
	if len(conn.announced) != 1 || conn.announced[0] != 1|1<<features.Follows {
		t.Errorf("SendFeatures carried %v, want the whole set with bits 0 and 8", conn.announced)
	}
}
