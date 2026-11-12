package main

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/edge"
	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/identity"
	"github.com/rodrigogaraujo/lanhop/internal/pairing"
	"github.com/rodrigogaraujo/lanhop/internal/sharer"
	"github.com/rodrigogaraujo/lanhop/internal/transport"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

const (
	keyLeftCtrl uint16 = 29
	keyLeftAlt  uint16 = 56
)

var notInput = event.Event{Type: 0x04, Code: 4}

type inputByHand struct {
	events chan event.Event
	taken  atomic.Bool
}

func (i *inputByHand) Events() <-chan event.Event {
	return i.events
}

func (i *inputByHand) Take() error {
	i.taken.Store(true)
	return nil
}

func (i *inputByHand) GiveBack() error {
	i.taken.Store(false)
	return nil
}

func (i *inputByHand) captures(t *testing.T, events ...event.Event) {
	t.Helper()
	for _, e := range append(slices.Clone(events), notInput) {
		select {
		case i.events <- e:
		case <-time.After(testPatience):
			t.Fatalf("share did not read %+v", e)
		}
	}
}

func (i *inputByHand) pressesHotkeyUntilFocusLeaves(t *testing.T, machine int) {
	t.Helper()
	digit := uint16(machine) + 1
	for deadline := time.Now().Add(testPatience); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		i.captures(t,
			event.Key(keyLeftCtrl, event.KeyPressed), event.Sync(),
			event.Key(keyLeftAlt, event.KeyPressed), event.Sync(),
			event.Key(digit, event.KeyPressed), event.Sync(),
			event.Key(digit, event.KeyReleased), event.Sync(),
			event.Key(keyLeftAlt, event.KeyReleased), event.Sync(),
			event.Key(keyLeftCtrl, event.KeyReleased), event.Sync(),
		)
		if i.taken.Load() {
			return
		}
	}
	t.Fatalf("the hotkey for machine %d never took the input away", machine)
}

type runningShare struct {
	input    *inputByHand
	output   *screen
	address  string
	stop     context.CancelFunc
	finished chan error
}

func startShare(t *testing.T) runningShare {
	t.Helper()
	input := &inputByHand{events: make(chan event.Event)}
	return startShareCapturing(t, input, input)
}

func startShareCapturing(t *testing.T, input *inputByHand, capture sharer.Capture) runningShare {
	t.Helper()
	done, stop := context.WithCancel(context.Background())
	started := runningShare{
		input:    input,
		output:   &screen{},
		stop:     stop,
		finished: make(chan error, 1),
	}
	go func() { started.finished <- shareUntil(done, capture, "127.0.0.1:0", started.output) }()
	t.Cleanup(stop)
	started.address = net.JoinHostPort("127.0.0.1", started.output.portShown(t))
	return started
}

func (s *screen) waitUntilShown(t *testing.T, wanted string) {
	t.Helper()
	for deadline := time.Now().Add(testPatience); !strings.Contains(s.String(), wanted); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the command never printed %q, it printed %q", wanted, s.String())
		}
	}
}

type placement struct{ x, y int }

type inputWithACursor struct {
	*inputByHand
	at      placement
	desktop edge.Bounds
	placed  chan placement
}

func (i *inputWithACursor) Cursor() (x, y int, desktop edge.Bounds, known bool) {
	return i.at.x, i.at.y, i.desktop, true
}

func (i *inputWithACursor) PlaceCursor(x, y int) {
	i.placed <- placement{x, y}
}

func (i *inputByHand) pushesUntilTheInputIs(t *testing.T, taken bool, dx int32) {
	t.Helper()
	for deadline := time.Now().Add(testPatience); i.taken.Load() != taken; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("pushing the cursor by %d never left the input taken = %v", dx, taken)
		}
		i.captures(t, event.Relative(event.RelX, dx), event.Sync())
	}
}

func dialShare(machine *identity.Identity, sharer *identity.Identity, address string) (*transport.Conn, error) {
	peer := trust.Peer{Name: "sharer", Fingerprint: sharer.Fingerprint, Address: address}
	return transport.Dial(context.Background(), transport.Config{Identity: machine}, peer)
}

func received(t *testing.T, conn *transport.Conn, count int) []event.Event {
	t.Helper()
	got := make([]event.Event, 0, count)
	for len(got) < count {
		next, err := conn.Receive()
		if err != nil {
			t.Fatalf("received only %+v, then %v", got, err)
		}
		got = append(got, next)
	}
	return got
}

func TestShareForwardsWhatIsCapturedToThePairedMachineWithFocusUntilItIsStopped(t *testing.T) {
	directory := useConfigDirectory(t)
	local := identityIn(t, directory)
	desk := identityIn(t, t.TempDir())
	trustAlready(t, directory, trust.Peer{Name: "desk", Fingerprint: desk.Fingerprint})
	share := startShare(t)
	conn, err := dialShare(desk, local, share.address)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	share.input.pressesHotkeyUntilFocusLeaves(t, 2)
	share.input.captures(t, event.Key(event.KeyA, event.KeyPressed), event.Sync())
	share.stop()
	stopped := await(t, share.finished)

	want := []event.Event{
		event.Enter(),
		event.Key(event.KeyA, event.KeyPressed), event.Sync(),
		event.Key(event.KeyA, event.KeyReleased), event.Sync(),
		event.Leave(),
	}
	if got := received(t, conn, len(want)); !slices.Equal(got, want) {
		t.Errorf("the paired machine received %+v, want %+v", got, want)
	}
	if after, err := conn.Receive(); err == nil {
		t.Errorf("the paired machine received %+v after share stopped, want a closed connection", after)
	}
	if stopped != nil {
		t.Errorf("share stopped with %v, want a clean stop", stopped)
	}
	if share.input.taken.Load() {
		t.Error("share stopped with the input taken away from this machine")
	}
	if again, err := dialShare(desk, local, share.address); err == nil {
		again.Close()
		t.Error("share still accepts connections after it stopped")
	}
}

func TestShareRefusesAMachineThatIsNotPaired(t *testing.T) {
	directory := useConfigDirectory(t)
	local := identityIn(t, directory)
	trustAlready(t, directory, trustedLaptop)
	share := startShare(t)

	conn, err := dialShare(identityIn(t, t.TempDir()), local, share.address)

	if err == nil {
		conn.Close()
		t.Error("a machine that is not paired connected to share")
	}
}

func TestMachineForgottenWhileShareRunsIsDisconnectedAndRefused(t *testing.T) {
	directory := useConfigDirectory(t)
	local := identityIn(t, directory)
	desk := identityIn(t, t.TempDir())
	trustAlready(t, directory, trust.Peer{Name: "desk", Fingerprint: desk.Fingerprint})
	share := startShare(t)
	conn, err := dialShare(desk, local, share.address)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	share.input.pressesHotkeyUntilFocusLeaves(t, 2)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"forget", "desk"}, nobodyTyping(), &stdout, &stderr); code != 0 {
		t.Fatalf("forget: exit code = %d, stderr = %q", code, stderr.String())
	}

	if got := received(t, conn, 1); !slices.Equal(got, []event.Event{event.Enter()}) {
		t.Errorf("the machine received %+v before it was forgotten, want only enter", got)
	}
	if after, err := conn.Receive(); err == nil {
		t.Errorf("the forgotten machine received %+v, want a closed connection", after)
	}
	for deadline := time.Now().Add(testPatience); share.input.taken.Load(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the input stayed taken away after the machine with focus was forgotten")
		}
	}
	if again, err := dialShare(desk, local, share.address); err == nil {
		again.Close()
		t.Error("the forgotten machine connected again")
	}
}

func TestMachinePairedWhileShareRunsConnectsAndGetsTheNextNumber(t *testing.T) {
	directory := useConfigDirectory(t)
	local := identityIn(t, directory)
	trustAlready(t, directory, trustedLaptop)
	share := startShare(t)
	desk := identityIn(t, t.TempDir())

	trustAlready(t, directory, trust.Peer{Name: "desk", Fingerprint: desk.Fingerprint})
	conn, err := dialShare(desk, local, share.address)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	share.input.pressesHotkeyUntilFocusLeaves(t, 3)

	if got := received(t, conn, 1); !slices.Equal(got, []event.Event{event.Enter()}) {
		t.Errorf("the new machine received %+v, want enter", got)
	}
}

func TestShareDoesNotStartWithADamagedListOfTrustedMachines(t *testing.T) {
	directory := useConfigDirectory(t)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, peersFileName), []byte("{"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	input := &inputByHand{events: make(chan event.Event)}

	err := shareUntil(context.Background(), input, "127.0.0.1:0", &screen{})

	if err == nil || !strings.Contains(err.Error(), "is damaged") {
		t.Errorf("shareUntil = %v, want an error that says the file is damaged", err)
	}
}

func TestShareDoesNotStartWhenItsPortIsTaken(t *testing.T) {
	useConfigDirectory(t)
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer taken.Close()
	input := &inputByHand{events: make(chan event.Event)}

	err = shareUntil(context.Background(), input, taken.Addr().String(), &screen{})

	var cannotListen *net.OpError
	if !errors.As(err, &cannotListen) || cannotListen.Op != "listen" {
		t.Errorf("shareUntil = %v, want the error of listening on a taken port", err)
	}
}

func TestShareSaysWhichPortItUsesAndWhichHotkeyGivesFocusToWhichMachine(t *testing.T) {
	useConfigDirectory(t)
	output := &screen{}
	done, stop := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() {
		finished <- shareUntil(done, &inputByHand{events: make(chan event.Event)}, "127.0.0.1:0", output)
	}()
	port := output.portShown(t)
	stop()
	await(t, finished)

	for _, want := range []string{"on port " + port, "Ctrl+Alt+1  brings them back to this machine", "Ctrl+Alt+2  gives them to the first machine", "Ctrl+C"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("share printed %q, which lacks %q", output.String(), want)
		}
	}
}

func TestPeersOnDiskFollowTheFileAndADamagedFileTrustsNobody(t *testing.T) {
	directory := useConfigDirectory(t)
	path := filepath.Join(directory, peersFileName)
	onDisk := peersOnDisk(path)
	desk := trust.Peer{Name: "desk", Fingerprint: someFingerprint(7)}
	expect := func(when string, want ...trust.Peer) {
		t.Helper()
		if got := onDisk.Peers(); !slices.Equal(got, want) {
			t.Errorf("%s, Peers = %+v, want %+v", when, got, want)
		}
		for _, peer := range []trust.Peer{trustedLaptop, desk} {
			found, trusted := onDisk.Find(peer.Fingerprint)
			if wanted := slices.Contains(want, peer); trusted != wanted || (wanted && found != peer) {
				t.Errorf("%s, Find(%s) = %+v, %v, want trusted = %v", when, peer.Name, found, trusted, wanted)
			}
		}
	}

	expect("before the file exists")
	trustAlready(t, directory, trustedLaptop, desk)
	expect("after two machines were paired", trustedLaptop, desk)
	if err := trustedIn(t, directory).Remove(trustedLaptop.Name); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	expect("after one was forgotten", desk)
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	expect("with a damaged file")
}

func TestShareUsedWronglyShowsUsageAndTouchesNothing(t *testing.T) {
	cases := map[string][]string{
		"share with an argument":       {"share", "192.168.0.10"},
		"share with an unknown option": {"share", "--port", "1234"},
		"share that listens nowhere":   {"share", "--listen"},
	}
	for label, args := range cases {
		t.Run(label, func(t *testing.T) {
			directory := useConfigDirectory(t)
			var stdout, stderr bytes.Buffer

			if code := run(args, nobodyTyping(), &stdout, &stderr); code != 2 {
				t.Errorf("exit code = %d, want 2", code)
			}
			if !strings.Contains(stderr.String(), "Usage:") {
				t.Errorf("stderr = %q, want the usage", stderr.String())
			}
			if _, err := os.Stat(directory); !os.IsNotExist(err) {
				t.Errorf("the config directory was created: %v", err)
			}
		})
	}
}

func TestHelpNamesTheShareCommandItsOptionAndItsPort(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"help"}, nobodyTyping(), &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	for _, mention := range []string{"lanhop share ", "Option of share:", "port " + strconv.Itoa(transport.DefaultPort)} {
		if !strings.Contains(stdout.String(), mention) {
			t.Errorf("help does not mention %q", mention)
		}
	}
}

func TestShareWithoutALayoutFileSaysThatEdgeSwitchingIsOffWhereTheFileGoesAndThatTheHotkeysWork(t *testing.T) {
	directory := useConfigDirectory(t)

	share := startShare(t)

	share.output.waitUntilShown(t, "Edge switching is off until a layout exists. Write one in "+filepath.Join(directory, layoutFileName)+"\n")
	share.output.waitUntilShown(t, "The hotkeys work without it.")
}

func TestShareWithALayoutSaysThatTheEdgesOfThisMachineDoNotCrossWhenItCannotFindItsCursor(t *testing.T) {
	directory := useConfigDirectory(t)
	trustAlready(t, directory, deskNextDoor)
	layoutFileIn(directory).save(t, pairing.OwnName()+" "+deskNextDoor.Name+"\n")

	share := startShare(t)

	share.output.waitUntilShown(t, "Edge switching is on between the machines this one controls, with the layout in "+filepath.Join(directory, layoutFileName))
	share.output.waitUntilShown(t, "The edges of this machine's own screen do not cross, because lanhop cannot find the cursor of this machine on this system yet.")
	expectNotShown(t, share.output, "Edge switching is off")
}

func TestShareWithALayoutFileThatHasProblemsSaysThemWithTheirLinesAndStillShares(t *testing.T) {
	directory := useConfigDirectory(t)
	local := identityIn(t, directory)
	desk := identityIn(t, t.TempDir())
	trustAlready(t, directory, trust.Peer{Name: deskNextDoor.Name, Fingerprint: desk.Fingerprint})
	layoutFileIn(directory).save(t, "# my desk\n"+deskNextDoor.Name+" stranger\n")

	share := startShare(t)
	conn, err := dialShare(desk, local, share.address)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	share.input.pressesHotkeyUntilFocusLeaves(t, 2)

	share.output.waitUntilShown(t, `  line 2: "stranger" is not a known machine`)
	share.output.waitUntilShown(t, "Edge switching is off until they are fixed.")
	if got := received(t, conn, 1); !slices.Equal(got, []event.Event{event.Enter()}) {
		t.Errorf("the paired machine received %+v after the hotkey, want only enter", got)
	}
}

func TestShareUsesALayoutWrittenWhileItRunsAndPushingAnEdgeMovesFocusToTheNeighbourAndBack(t *testing.T) {
	directory := useConfigDirectory(t)
	local := identityIn(t, directory)
	desk := identityIn(t, t.TempDir())
	trustAlready(t, directory, trust.Peer{Name: deskNextDoor.Name, Fingerprint: desk.Fingerprint})
	input := &inputWithACursor{
		inputByHand: &inputByHand{events: make(chan event.Event)},
		at:          placement{2000, 250},
		desktop:     edge.Bounds{MinX: 0, MinY: 0, MaxX: 2000, MaxY: 1000},
		placed:      make(chan placement, 1),
	}
	share := startShareCapturing(t, input.inputByHand, input)
	conn, err := dialShare(desk, local, share.address)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	share.output.waitUntilShown(t, "Edge switching is off until a layout exists")
	atTheLeftEdge := []event.Event{event.Absolute(event.AbsX, 0), event.Absolute(event.AbsY, 16384), event.Sync()}

	layoutFileIn(directory).save(t, pairing.OwnName()+" "+deskNextDoor.Name+"\n")
	share.output.waitUntilShown(t, "Edge switching is on, with the layout in "+filepath.Join(directory, layoutFileName))
	input.pushesUntilTheInputIs(t, true, 1)

	if got, want := received(t, conn, 4), append([]event.Event{event.Enter()}, atTheLeftEdge...); !slices.Equal(got, want) {
		t.Fatalf("the neighbour on the right received %+v, want %+v", got, want)
	}

	input.pushesUntilTheInputIs(t, false, -1)

	for pushed := 0; ; pushed++ {
		next := received(t, conn, 1)[0]
		if next == event.Leave() {
			break
		}
		if want := atTheLeftEdge[pushed%len(atTheLeftEdge)]; next != want {
			t.Fatalf("while its left edge was pushed the neighbour received %+v, want %+v", next, want)
		}
	}
	if back := await(t, input.placed); back != (placement{2000, 250}) {
		t.Errorf("the cursor of this machine was placed at %+v, want it back on the right edge at the height it left", back)
	}
}
