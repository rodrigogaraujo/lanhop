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

	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/identity"
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
	address  string
	stop     context.CancelFunc
	finished chan error
}

func startShare(t *testing.T) runningShare {
	t.Helper()
	output := &screen{}
	done, stop := context.WithCancel(context.Background())
	started := runningShare{
		input:    &inputByHand{events: make(chan event.Event)},
		stop:     stop,
		finished: make(chan error, 1),
	}
	go func() { started.finished <- shareUntil(done, started.input, "127.0.0.1:0", output) }()
	t.Cleanup(stop)
	started.address = net.JoinHostPort("127.0.0.1", output.portShown(t))
	return started
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
