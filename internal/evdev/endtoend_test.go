//go:build linux && (386 || amd64 || arm || arm64 || loong64 || riscv64)

package evdev

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/rodrigogaraujo/lanhop/internal/edge"
	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/hotkey"
	"github.com/rodrigogaraujo/lanhop/internal/identity"
	"github.com/rodrigogaraujo/lanhop/internal/layout"
	"github.com/rodrigogaraujo/lanhop/internal/receiver"
	"github.com/rodrigogaraujo/lanhop/internal/sharer"
	"github.com/rodrigogaraujo/lanhop/internal/transport"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

const (
	peersFileName              = "peers.json"
	sharingMachineName         = "desk"
	sharedDevices              = 2
	replayedEventsKept         = 1 << 14
	heldKeysQuery      uintptr = 0x18
)

type machine struct {
	directory string
	identity  *identity.Identity
}

func newMachine(t *testing.T) machine {
	t.Helper()
	directory := t.TempDir()
	created, err := identity.LoadOrCreate(directory)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	return machine{directory: directory, identity: created}
}

func (m machine) knownAs(name string) trust.Peer {
	return trust.Peer{Name: name, Fingerprint: m.identity.Fingerprint}
}

func (m machine) reachedAt(name, address string) trust.Peer {
	return trust.Peer{Name: name, Fingerprint: m.identity.Fingerprint, Address: address}
}

func (m machine) trusting(t *testing.T, peers ...trust.Peer) *trust.Store {
	t.Helper()
	trusted, err := trust.Open(filepath.Join(m.directory, peersFileName))
	if err != nil {
		t.Fatalf("trust.Open: %v", err)
	}
	for _, peer := range peers {
		if err := trusted.Add(peer); err != nil {
			t.Fatalf("trust %q: %v", peer.Name, err)
		}
	}
	return trusted
}

func runUntilTheTestEnds(t *testing.T, who string, run func(context.Context) error) {
	t.Helper()
	ctx, stop := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() { stopped <- run(ctx) }()
	t.Cleanup(func() {
		stop()
		select {
		case err := <-stopped:
			if err != nil {
				t.Errorf("%s stopped with %v", who, err)
			}
		case <-time.After(capturePatience):
			t.Errorf("%s kept running after the test ended", who)
		}
	})
}

type sharingMachine struct {
	machine
	keyboard     *VirtualDevice
	keyboardNode string
	mouse        *VirtualDevice
	capture      *Capture
	address      string
	connected    chan string
	refusals     chan error
}

func startSharing(t *testing.T, receivers ...trust.Peer) *sharingMachine {
	t.Helper()
	return startSharingOnDesk(t, nil, receivers...)
}

func deskDrawnAs(t *testing.T, drawing string) *layout.Layout {
	t.Helper()
	drawn, err := layout.Parse(strings.NewReader(drawing), func(string) bool { return true })
	if err != nil {
		t.Fatalf("the layout %q of the test has problems: %v", drawing, err)
	}
	return drawn
}

func startSharingOnDesk(t *testing.T, drawn *layout.Layout, receivers ...trust.Peer) *sharingMachine {
	t.Helper()
	devices := t.TempDir()
	keyboard, keyboardNode := plugInto(t, devices, usbKeyboard)
	mouse, _ := plugInto(t, devices, usbMouse)
	desk := &sharingMachine{
		machine:      newMachine(t),
		keyboard:     keyboard,
		keyboardNode: keyboardNode,
		mouse:        mouse,
		capture:      startCaptureIn(t, devices),
		connected:    make(chan string, 8),
		refusals:     make(chan error, 8),
	}
	trusted := desk.trusting(t, receivers...)
	detector, err := hotkey.New(hotkey.DefaultModifiers)
	if err != nil {
		t.Fatalf("hotkey.New: %v", err)
	}
	listener, err := transport.Listen("127.0.0.1:0", transport.Config{Identity: desk.identity, OnReject: desk.refused}, trusted)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	desk.address = listener.Addr().String()

	focus := sharer.New(desk.capture, trusted, detector, sharer.EdgeSwitching{
		Name:   sharingMachineName,
		Layout: func() *layout.Layout { return drawn },
		Speed:  sharer.DefaultSpeed(),
		Watch:  edge.DefaultSettings(),
	})
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			focus.Connect(conn)
			desk.connected <- conn.Peer().Name
		}
	}()
	runUntilTheTestEnds(t, "the sharer", focus.Run)
	return desk
}

func (s *sharingMachine) refused(_ net.Addr, reason error) {
	select {
	case s.refusals <- reason:
	default:
	}
}

func (s *sharingMachine) waitUntilConnected(t *testing.T, name string) {
	t.Helper()
	select {
	case connected := <-s.connected:
		if connected != name {
			t.Fatalf("%q connected to the sharer, want %q", connected, name)
		}
	case <-time.After(capturePatience):
		t.Fatalf("%q did not connect to the sharer", name)
	}
}

func hotkeyOfMachine(digit uint16) []event.Event {
	return []event.Event{
		event.Key(keyLeftCtrl, event.KeyPressed), event.Sync(),
		event.Key(keyLeftAlt, event.KeyPressed), event.Sync(),
		event.Key(digit, event.KeyPressed), event.Sync(),
		event.Key(digit, event.KeyReleased), event.Sync(),
		event.Key(keyLeftAlt, event.KeyReleased), event.Sync(),
		event.Key(keyLeftCtrl, event.KeyReleased), event.Sync(),
	}
}

func (s *sharingMachine) devicesTakenAway() int {
	s.capture.mutex.Lock()
	defer s.capture.mutex.Unlock()
	taken := 0
	for _, source := range s.capture.sources {
		if source.grabbed {
			taken++
		}
	}
	return taken
}

func (s *sharingMachine) waitUntilDevicesTakenAway(t *testing.T, want int) {
	t.Helper()
	for deadline := time.Now().Add(capturePatience); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		if s.devicesTakenAway() == want {
			return
		}
	}
	t.Fatalf("the sharer keeps %d devices away from its own desktop, want %d", s.devicesTakenAway(), want)
}

func (s *sharingMachine) giveFocusToTheFirstReceiver(t *testing.T) {
	t.Helper()
	emit(t, s.keyboard, hotkeyOfMachine(keyDigit2)...)
	s.waitUntilDevicesTakenAway(t, sharedDevices)
}

func (s *sharingMachine) takeFocusBack(t *testing.T) {
	t.Helper()
	emit(t, s.keyboard, hotkeyOfMachine(keyDigit1)...)
	s.waitUntilDevicesTakenAway(t, 0)
}

type replayedEvent struct {
	event.Event
	at time.Time
}

type receivingMachine struct {
	node               string
	otherReader        *Device
	otherPointerReader *Device
	replayed           chan replayedEvent
	positioned         chan replayedEvent
	failures           chan error
	connected          atomic.Bool
}

func readByTheTestAlone(t *testing.T, node string) (read chan replayedEvent, otherReader *Device) {
	t.Helper()
	onlyReader := openAsAnotherReader(t, node)
	if err := onlyReader.Grab(); err != nil {
		t.Fatalf("keep what the receiver replays away from the machine running the tests: %v", err)
	}
	read = make(chan replayedEvent, replayedEventsKept)
	go func() {
		for {
			next, err := onlyReader.Read()
			if err != nil {
				return
			}
			read <- replayedEvent{Event: next, at: time.Now()}
		}
	}()
	return read, openAsAnotherReader(t, node)
}

func (m machine) receiveFrom(t *testing.T, sharing trust.Peer) *receivingMachine {
	t.Helper()
	trusted := m.trusting(t, sharing)
	replay := createReplay(t)
	controlled := &receivingMachine{
		node:     eventNode(t, replay.keyboardAndMouse),
		failures: make(chan error, 8),
	}
	controlled.replayed, controlled.otherReader = readByTheTestAlone(t, controlled.node)
	controlled.positioned, controlled.otherPointerReader = readByTheTestAlone(t, eventNode(t, replay.pointer))
	runUntilTheTestEnds(t, "the receiver", func(ctx context.Context) error {
		return receiver.Run(ctx, receiver.Config{
			Transport: transport.Config{Identity: m.identity},
			Sharer:    trusted.Peers()[0],
			Replay:    replay,
			OnConnect: func() { controlled.connected.Store(true) },
			OnFailure: controlled.failed,
		})
	})
	return controlled
}

func (r *receivingMachine) failed(reason error) {
	select {
	case r.failures <- reason:
	default:
	}
}

func (r *receivingMachine) nextReplayed(t *testing.T) replayedEvent {
	t.Helper()
	select {
	case next := <-r.replayed:
		return next
	case <-time.After(capturePatience):
		t.Fatal("the receiver's device replayed nothing more")
		return replayedEvent{}
	}
}

func (r *receivingMachine) expectReplayed(t *testing.T, want ...event.Event) {
	t.Helper()
	for i, wanted := range want {
		if got := r.nextReplayed(t).Event; got != wanted {
			t.Fatalf("event %d of %d replayed on the receiver's device = %+v, want %+v", i+1, len(want), got, wanted)
		}
	}
}

func (r *receivingMachine) expectNothingReplayed(t *testing.T) {
	t.Helper()
	select {
	case unexpected := <-r.replayed:
		t.Errorf("the receiver's device replayed %+v, want nothing", unexpected.Event)
	case unexpected := <-r.positioned:
		t.Errorf("the receiver's pointer replayed %+v, want nothing", unexpected.Event)
	case <-time.After(quietPeriod):
	}
}

func (r *receivingMachine) expectPositioned(t *testing.T, want ...event.Event) {
	t.Helper()
	for i, wanted := range want {
		select {
		case got := <-r.positioned:
			if got.Event != wanted {
				t.Fatalf("event %d of %d replayed on the receiver's pointer = %+v, want %+v", i+1, len(want), got.Event, wanted)
			}
		case <-time.After(capturePatience):
			t.Fatalf("the receiver's pointer replayed nothing more, want %+v", wanted)
		}
	}
}

func (r *receivingMachine) positionedUntilQuiet() []event.Event {
	var positioned []event.Event
	for {
		select {
		case next := <-r.positioned:
			positioned = append(positioned, next.Event)
		case <-time.After(quietPeriod):
			return positioned
		}
	}
}

func (r *receivingMachine) expectReplayHiddenFromOtherReaders(t *testing.T) {
	t.Helper()
	for device, otherReader := range map[string]*Device{"device": r.otherReader, "pointer": r.otherPointerReader} {
		if seen, err := readWithin(t, otherReader, quietPeriod); !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Errorf("another reader of the receiver's %s got %+v, %v, want nothing: the machine running the tests would act on what is replayed", device, seen, err)
		}
	}
}

func (r *receivingMachine) expectHeld(t *testing.T, want ...uint16) {
	t.Helper()
	device := openAsAnotherReader(t, r.node)
	held := make(codeSet, keyCodeCount/8)
	query := request(directionRead, 'E', heldKeysQuery, uintptr(len(held)))
	if err := ioctlPointer(device.file, query, unsafe.Pointer(&held[0])); err != nil {
		t.Fatalf("ask the kernel which keys are held on the receiver's device: %v", err)
	}
	var got []uint16
	for code := uint16(0); code < keyCodeCount; code++ {
		if held.has(code) {
			got = append(got, code)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("keys held on the receiver's device = %v, want %v", got, want)
	}
}

func sharerAndItsPairedReceiver(t *testing.T) (*sharingMachine, *receivingMachine) {
	t.Helper()
	laptop := newMachine(t)
	desk := startSharing(t, laptop.knownAs("laptop"))
	controlled := laptop.receiveFrom(t, desk.reachedAt("desk", desk.address))
	desk.waitUntilConnected(t, "laptop")
	return desk, controlled
}

type cable struct {
	listener  net.Listener
	unplugged atomic.Bool
}

func plugCableTo(t *testing.T, address string) *cable {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	plugged := &cable{listener: listener}
	go func() {
		near, err := listener.Accept()
		if err != nil {
			return
		}
		far, err := net.Dial("tcp", address)
		if err != nil {
			near.Close()
			return
		}
		go plugged.carry(far, near)
		plugged.carry(near, far)
	}()
	return plugged
}

func (c *cable) carry(to, from net.Conn) {
	defer to.Close()
	buffer := make([]byte, 4096)
	for {
		length, err := from.Read(buffer)
		if err != nil {
			return
		}
		if c.unplugged.Load() {
			continue
		}
		if _, err := to.Write(buffer[:length]); err != nil {
			return
		}
	}
}

func (c *cable) unplug() {
	c.unplugged.Store(true)
	c.listener.Close()
}

func TestFocusFollowsTheHotkeyAndTheDigitOfTheHotkeyNeverReachesTheReceiver(t *testing.T) {
	desk, controlled := sharerAndItsPairedReceiver(t)
	typedA := []event.Event{event.Key(event.KeyA, event.KeyPressed), event.Sync(), event.Key(event.KeyA, event.KeyReleased), event.Sync()}
	nudged := []event.Event{event.Relative(event.RelX, 1), event.Sync()}

	emit(t, desk.keyboard, typedA...)
	emit(t, desk.mouse, nudged...)
	controlled.expectNothingReplayed(t)

	desk.giveFocusToTheFirstReceiver(t)
	for _, step := range []struct {
		device *VirtualDevice
		report []event.Event
		held   []uint16
	}{
		{desk.keyboard, []event.Event{event.Key(event.KeyA, event.KeyPressed), event.Sync()}, []uint16{event.KeyA}},
		{desk.mouse, []event.Event{event.Key(event.ButtonLeft, event.KeyPressed), event.Sync()}, []uint16{event.KeyA, event.ButtonLeft}},
		{desk.mouse, []event.Event{event.Relative(event.RelX, 2), event.Relative(event.RelY, -1), event.Sync()}, []uint16{event.KeyA, event.ButtonLeft}},
		{desk.mouse, []event.Event{event.Relative(event.RelWheel, 1), event.Sync()}, []uint16{event.KeyA, event.ButtonLeft}},
		{desk.mouse, []event.Event{event.Key(event.ButtonLeft, event.KeyReleased), event.Sync()}, []uint16{event.KeyA}},
		{desk.keyboard, []event.Event{event.Key(event.KeyA, event.KeyReleased), event.Sync()}, nil},
	} {
		emit(t, step.device, step.report...)
		controlled.expectReplayed(t, step.report...)
		controlled.expectHeld(t, step.held...)
	}

	desk.takeFocusBack(t)
	controlled.expectReplayed(t,
		event.Key(keyLeftCtrl, event.KeyPressed), event.Sync(),
		event.Key(keyLeftAlt, event.KeyPressed), event.Sync(),
		event.Key(keyLeftAlt, event.KeyReleased), event.Sync(),
		event.Key(keyLeftCtrl, event.KeyReleased), event.Sync(),
	)
	emit(t, desk.keyboard, typedA...)
	emit(t, desk.mouse, nudged...)
	controlled.expectNothingReplayed(t)
	controlled.expectHeld(t)
	controlled.expectReplayHiddenFromOtherReaders(t)
}

func TestAThousandKeysAndHundredsOfMovementsAreReplayedOnceAndInOrder(t *testing.T) {
	const (
		rounds            = 100
		keysPerRound      = 10
		movementsPerRound = 3
	)
	desk, controlled := sharerAndItsPairedReceiver(t)
	desk.giveFocusToTheFirstReceiver(t)

	for round := range rounds {
		var typed, moved []event.Event
		for range keysPerRound {
			typed = append(typed, event.Key(event.KeyA, event.KeyPressed), event.Sync(), event.Key(event.KeyA, event.KeyReleased), event.Sync())
		}
		for movement := range movementsPerRound {
			step := int32(round*movementsPerRound + movement)
			moved = append(moved, event.Relative(event.RelX, 1+step%3), event.Relative(event.RelY, -1-step%2), event.Sync())
		}

		emit(t, desk.keyboard, typed...)
		controlled.expectReplayed(t, typed...)
		emit(t, desk.mouse, moved...)
		controlled.expectReplayed(t, moved...)
	}

	controlled.expectNothingReplayed(t)
	controlled.expectHeld(t)
	controlled.expectReplayHiddenFromOtherReaders(t)
}

func TestMachineTheSharerIsNotPairedWithCannotConnectAndReplaysNothing(t *testing.T) {
	laptop, stranger := newMachine(t), newMachine(t)
	desk := startSharing(t, laptop.knownAs("laptop"))

	uninvited := stranger.receiveFrom(t, desk.reachedAt("desk", desk.address))
	select {
	case reason := <-desk.refusals:
		if !errors.Is(reason, transport.ErrUntrusted) {
			t.Errorf("the sharer refused the unpaired machine with %v, want ErrUntrusted", reason)
		}
	case <-time.After(capturePatience):
		t.Fatal("the sharer did not refuse the unpaired machine")
	}
	controlled := laptop.receiveFrom(t, desk.reachedAt("desk", desk.address))
	desk.waitUntilConnected(t, "laptop")
	desk.giveFocusToTheFirstReceiver(t)
	typedA := []event.Event{event.Key(event.KeyA, event.KeyPressed), event.Sync(), event.Key(event.KeyA, event.KeyReleased), event.Sync()}
	emit(t, desk.keyboard, typedA...)
	controlled.expectReplayed(t, typedA...)

	uninvited.expectNothingReplayed(t)
	if uninvited.connected.Load() {
		t.Error("the unpaired machine connected to the sharer")
	}
}

func TestReceiverRefusesAMachineThatIsNotItsSharerAndReplaysNothing(t *testing.T) {
	laptop, desk := newMachine(t), newMachine(t)
	impostor := startSharing(t, laptop.knownAs("laptop"))

	controlled := laptop.receiveFrom(t, desk.reachedAt("desk", impostor.address))
	select {
	case reason := <-controlled.failures:
		if !errors.Is(reason, transport.ErrUntrusted) {
			t.Errorf("the receiver refused the machine that is not its sharer with %v, want ErrUntrusted", reason)
		}
	case <-time.After(capturePatience):
		t.Fatal("the receiver did not refuse the machine that is not its sharer")
	}
	emit(t, impostor.keyboard, hotkeyOfMachine(keyDigit2)...)
	emit(t, impostor.keyboard, event.Key(event.KeyA, event.KeyPressed), event.Sync(), event.Key(event.KeyA, event.KeyReleased), event.Sync())

	controlled.expectNothingReplayed(t)
	if controlled.connected.Load() {
		t.Error("the receiver connected to a machine that is not its sharer")
	}
}

func TestCableUnpluggedWithAKeyHeldGivesTheInputBackAndReleasesTheKeyOnTheReceiver(t *testing.T) {
	const (
		controlBackLimit    = 3 * time.Second
		roomForASlowMachine = time.Second
	)
	laptop := newMachine(t)
	desk := startSharing(t, laptop.knownAs("laptop"))
	network := plugCableTo(t, desk.address)
	controlled := laptop.receiveFrom(t, desk.reachedAt("desk", network.listener.Addr().String()))
	desk.waitUntilConnected(t, "laptop")
	desktopOfTheSharer := openAsAnotherReader(t, desk.keyboardNode)
	desk.giveFocusToTheFirstReceiver(t)
	pressedA := []event.Event{event.Key(event.KeyA, event.KeyPressed), event.Sync()}
	releasedA := []event.Event{event.Key(event.KeyA, event.KeyReleased), event.Sync()}
	emit(t, desk.keyboard, pressedA...)
	controlled.expectReplayed(t, pressedA...)
	controlled.expectHeld(t, event.KeyA)

	unplugged := time.Now()
	network.unplug()

	desk.waitUntilDevicesTakenAway(t, 0)
	inputBackAfter := time.Since(unplugged)
	release := controlled.nextReplayed(t)
	if release.Event != releasedA[0] {
		t.Fatalf("the receiver's device replayed %+v after the cable was unplugged, want the release of A", release.Event)
	}
	controlled.expectReplayed(t, event.Sync())
	keyReleasedAfter := release.at.Sub(unplugged)
	t.Logf("after the cable was unplugged, the sharer gave the input back in %s and the receiver released the held key in %s", inputBackAfter, keyReleasedAfter)
	if limit := controlBackLimit + roomForASlowMachine; inputBackAfter > limit || keyReleasedAfter > limit {
		t.Errorf("the input was back after %s and the key released after %s, want both within %s", inputBackAfter, keyReleasedAfter, limit)
	}
	controlled.expectHeld(t)

	emit(t, desk.keyboard, releasedA...)
	for i, want := range append(hotkeyOfMachine(keyDigit2), releasedA...) {
		if got, err := readWithin(t, desktopOfTheSharer, capturePatience); err != nil || got != want {
			t.Fatalf("event %d seen by the desktop of the sharer = %+v, %v, want %+v", i+1, got, err, want)
		}
	}
	controlled.expectNothingReplayed(t)
}

func TestKeysReachTheReceiversDeviceWithLittleAddedDelay(t *testing.T) {
	const (
		measuredKeys     = 300
		medianLimit      = 10 * time.Millisecond
		ninetyNinthLimit = 100 * time.Millisecond
	)
	desk, controlled := sharerAndItsPairedReceiver(t)
	desk.giveFocusToTheFirstReceiver(t)

	delays := make([]time.Duration, 0, measuredKeys)
	for i := range measuredKeys {
		key := event.Key(event.KeyA, event.KeyPressed)
		if i%2 == 1 {
			key = event.Key(event.KeyA, event.KeyReleased)
		}
		written := time.Now()
		emit(t, desk.keyboard, key, event.Sync())
		arrived := controlled.nextReplayed(t)
		if arrived.Event != key {
			t.Fatalf("key %d replayed on the receiver's device = %+v, want %+v", i+1, arrived.Event, key)
		}
		controlled.expectReplayed(t, event.Sync())
		delays = append(delays, arrived.at.Sub(written))
	}

	slices.Sort(delays)
	median, ninetyNinth := delays[measuredKeys/2], delays[measuredKeys*99/100]
	t.Logf("delay from writing a key on the fake keyboard to reading it on the receiver's device, over %d keys with both machines on this one: median %s, 99th percentile %s", measuredKeys, median, ninetyNinth)
	if median > medianLimit {
		t.Errorf("median delay = %s, want at most %s", median, medianLimit)
	}
	if ninetyNinth > ninetyNinthLimit {
		t.Errorf("99th percentile of the delay = %s, want at most %s", ninetyNinth, ninetyNinthLimit)
	}
}

func TestWithALayoutMovementReachesTheReceiverAsPositionsAndPushingItsEdgeBringsFocusBack(t *testing.T) {
	const (
		centre                        = 32767
		afterThreeUnitsToTheRight     = 32869
		afterOneUnitUp                = 32706
		stepTowardsTheLeftEdge        = -10
		afterTheFirstStepToTheLeft    = 32528
		stepsUntilTheLeftEdge         = 97
		pauseBetweenSteps             = 2 * time.Millisecond
		eventsOfAStepThatMovesOnlyOnX = 2
	)
	laptop := newMachine(t)
	desk := startSharingOnDesk(t, deskDrawnAs(t, "desk laptop"), laptop.knownAs("laptop"))
	controlled := laptop.receiveFrom(t, desk.reachedAt(sharingMachineName, desk.address))
	desk.waitUntilConnected(t, "laptop")

	desk.giveFocusToTheFirstReceiver(t)
	controlled.expectPositioned(t, event.Absolute(event.AbsX, centre), event.Absolute(event.AbsY, centre), event.Sync())

	emit(t, desk.mouse, event.Relative(event.RelX, 3), event.Relative(event.RelY, -1), event.Sync())
	controlled.expectPositioned(t, event.Absolute(event.AbsX, afterThreeUnitsToTheRight), event.Absolute(event.AbsY, afterOneUnitUp), event.Sync())

	steps := 0
	for pushingSince := time.Now(); desk.devicesTakenAway() != 0; time.Sleep(pauseBetweenSteps) {
		if time.Since(pushingSince) > capturePatience {
			t.Fatalf("after %d steps against the left edge of the receiver the sharer still keeps its devices away from its own desktop", steps)
		}
		emit(t, desk.mouse, event.Relative(event.RelX, stepTowardsTheLeftEdge), event.Sync())
		steps++
	}

	if steps <= stepsUntilTheLeftEdge {
		t.Errorf("focus came back after %d steps, before the cursor of the receiver could reach its left edge", steps)
	}
	positions := controlled.positionedUntilQuiet()
	if len(positions) != eventsOfAStepThatMovesOnlyOnX*stepsUntilTheLeftEdge {
		t.Fatalf("the receiver's pointer replayed %d events on the way to its left edge, want a position and the end of its report for each of the %d steps: %+v", len(positions), stepsUntilTheLeftEdge, positions)
	}
	if first := positions[0]; first != event.Absolute(event.AbsX, afterTheFirstStepToTheLeft) {
		t.Errorf("the first step to the left placed the pointer with %+v, want X at %d", first, afterTheFirstStepToTheLeft)
	}
	previous := int32(afterThreeUnitsToTheRight)
	for i := 0; i < len(positions); i += eventsOfAStepThatMovesOnlyOnX {
		placed, ended := positions[i], positions[i+1]
		if placed.Type != event.TypeAbsolute || placed.Code != event.AbsX || placed.Value >= previous || ended != event.Sync() {
			t.Fatalf("step %d replayed %+v and %+v on the receiver's pointer, want X to the left of %d and the end of the report", i/eventsOfAStepThatMovesOnlyOnX+1, placed, ended, previous)
		}
		previous = placed.Value
	}
	if previous != 0 {
		t.Errorf("the pointer of the receiver stopped at X %d, want its left edge at 0", previous)
	}

	emit(t, desk.mouse, event.Relative(event.RelX, 1), event.Sync())
	controlled.expectNothingReplayed(t)
	controlled.expectHeld(t)
	controlled.expectReplayHiddenFromOtherReaders(t)
}
