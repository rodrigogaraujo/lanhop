package main

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/identity"
	"github.com/rodrigogaraujo/lanhop/internal/pairing"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

const (
	testPatience    = 10 * time.Second
	loopbackSharing = "127.0.0.1:24745"
)

var waitingOnPort = regexp.MustCompile(`on port (\d+)`)

func useConfigDirectory(t *testing.T) string {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "lanhop")
	t.Setenv(configDirectoryVariable, directory)
	return directory
}

func trustedIn(t *testing.T, directory string) *trust.Store {
	t.Helper()
	store, err := trust.Open(filepath.Join(directory, peersFileName))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return store
}

func identityIn(t *testing.T, directory string) *identity.Identity {
	t.Helper()
	local, err := identity.LoadOrCreate(directory)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	return local
}

func someFingerprint(seed byte) identity.Fingerprint {
	var fingerprint identity.Fingerprint
	fingerprint[0] = seed
	return fingerprint
}

func trustAlready(t *testing.T, directory string, peers ...trust.Peer) {
	t.Helper()
	store := trustedIn(t, directory)
	for _, peer := range peers {
		if err := store.Add(peer); err != nil {
			t.Fatalf("Add(%+v): %v", peer, err)
		}
	}
}

type screen struct {
	mutex sync.Mutex
	text  bytes.Buffer
}

func (s *screen) Write(written []byte) (int, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.text.Write(written)
}

func (s *screen) String() string {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.text.String()
}

func (s *screen) portShown(t *testing.T) string {
	t.Helper()
	for deadline := time.Now().Add(testPatience); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if found := waitingOnPort.FindStringSubmatch(s.String()); found != nil {
			return found[1]
		}
	}
	t.Fatalf("the command never said which port it waits on, it printed %q", s.String())
	return ""
}

type answers bool

func (a answers) Confirm(pairing.Question) bool {
	return bool(a)
}

type otherMachine struct {
	identity *identity.Identity
	store    *trust.Store
	config   pairing.Config
}

func newOtherMachine(t *testing.T, name string, accepts bool) otherMachine {
	t.Helper()
	directory := t.TempDir()
	other := otherMachine{identity: identityIn(t, directory), store: trustedIn(t, directory)}
	other.config = pairing.Config{
		Identity: other.identity,
		Store:    other.store,
		Name:     name,
		Asker:    answers(accepts),
		Timeout:  testPatience,
	}
	return other
}

type pairingResult struct {
	peer trust.Peer
	err  error
}

func (m otherMachine) waits(t *testing.T) (string, <-chan pairingResult) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	finished := make(chan pairingResult, 1)
	go func() {
		peer, err := pairing.Accept(listener, m.config)
		finished <- pairingResult{peer, err}
	}()
	return listener.Addr().String(), finished
}

func await[T any](t *testing.T, finished <-chan T) T {
	t.Helper()
	select {
	case got := <-finished:
		return got
	case <-time.After(testPatience):
		t.Fatal("pairing did not finish")
		var nothing T
		return nothing
	}
}

type commandResult struct {
	exitCode int
	stderr   string
}

func startCommand(typed string, output *screen, args ...string) <-chan commandResult {
	finished := make(chan commandResult, 1)
	go func() {
		var stderr bytes.Buffer
		exitCode := run(args, strings.NewReader(typed), output, &stderr)
		finished <- commandResult{exitCode, stderr.String()}
	}()
	return finished
}

func ownHostnameAsProposed() string {
	hostname, _ := os.Hostname()
	return pairing.NameFrom(hostname)
}

func TestPairWithoutAnAddressWaitsAndTrustsTheMachineThatConnects(t *testing.T) {
	directory := useConfigDirectory(t)
	laptop := newOtherMachine(t, "laptop", true)
	output := &screen{}

	command := startCommand("yes\n", output, "pair", "--listen", "127.0.0.1:0")
	seenByLaptop, err := pairing.Dial(net.JoinHostPort("127.0.0.1", output.portShown(t)), laptop.config)
	finished := await(t, command)

	if err != nil || finished.exitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %q, the laptop got %v", finished.exitCode, finished.stderr, err)
	}
	local := identityIn(t, directory)
	if want := (trust.Peer{Name: ownHostnameAsProposed(), Fingerprint: local.Fingerprint, Address: loopbackSharing}); seenByLaptop != want {
		t.Errorf("the laptop trusts %+v, want %+v", seenByLaptop, want)
	}
	laptopHere := trust.Peer{Name: "laptop", Fingerprint: laptop.identity.Fingerprint}
	if got := trustedIn(t, directory).Peers(); !slices.Equal(got, []trust.Peer{laptopHere}) {
		t.Errorf("the command stored %+v, want only %+v", got, laptopHere)
	}
	shown := output.String()
	for _, want := range []string{
		"This machine:   " + local.Fingerprint.Code(),
		"Other machine:  " + laptop.identity.Fingerprint.Code(),
		`trusted here as "laptop"`,
		`Paired. "laptop" is now trusted.`,
	} {
		if !strings.Contains(shown, want) {
			t.Errorf("the command printed %q, which lacks %q", shown, want)
		}
	}
}

func TestPairWithAnAddressTrustsTheWaitingMachineAndKeepsItsAddress(t *testing.T) {
	directory := useConfigDirectory(t)
	desk := newOtherMachine(t, "desk", true)
	address, waited := desk.waits(t)
	output := &screen{}

	finished := await(t, startCommand("yes\n", output, "pair", address))
	seenByDesk := await(t, waited)

	if finished.exitCode != 0 || seenByDesk.err != nil {
		t.Fatalf("exit code = %d, stderr = %q, the desk got %v", finished.exitCode, finished.stderr, seenByDesk.err)
	}
	local := identityIn(t, directory)
	if want := (trust.Peer{Name: ownHostnameAsProposed(), Fingerprint: local.Fingerprint}); seenByDesk.peer != want {
		t.Errorf("the desk trusts %+v, want %+v", seenByDesk.peer, want)
	}
	deskHere := trust.Peer{Name: "desk", Fingerprint: desk.identity.Fingerprint, Address: loopbackSharing}
	if got := trustedIn(t, directory).Peers(); !slices.Equal(got, []trust.Peer{deskHere}) {
		t.Errorf("the command stored %+v, want only %+v", got, deskHere)
	}
	shown := output.String()
	for _, want := range []string{
		"This machine:   " + local.Fingerprint.Code(),
		"Other machine:  " + desk.identity.Fingerprint.Code(),
		`Paired. "desk" is now trusted, at ` + loopbackSharing,
	} {
		if !strings.Contains(shown, want) {
			t.Errorf("the command printed %q, which lacks %q", shown, want)
		}
	}
}

func TestPairKeepsTheKeyAndThePeersInsideTheConfigDirectory(t *testing.T) {
	directory := useConfigDirectory(t)
	address, waited := newOtherMachine(t, "desk", true).waits(t)

	finished := await(t, startCommand("yes\n", &screen{}, "pair", address))
	await(t, waited)

	if finished.exitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %q", finished.exitCode, finished.stderr)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if want := []string{"identity.pem", "peers.json"}; !slices.Equal(names, want) {
		t.Errorf("%s holds %v, want %v", directory, names, want)
	}
}

func TestPairStoresNothingUnlessThePersonTypesYes(t *testing.T) {
	for _, typed := range []string{"no\n", "\n", "", "y\n", "yes please\n"} {
		t.Run(fmt.Sprintf("typing %q", typed), func(t *testing.T) {
			directory := useConfigDirectory(t)
			desk := newOtherMachine(t, "desk", true)
			address, waited := desk.waits(t)

			finished := await(t, startCommand(typed, &screen{}, "pair", address))
			seenByDesk := await(t, waited)

			if finished.exitCode != 1 || !strings.Contains(finished.stderr, pairing.ErrDeclined.Error()) {
				t.Errorf("exit code = %d, stderr = %q, want 1 and the refusal", finished.exitCode, finished.stderr)
			}
			if seenByDesk.err == nil {
				t.Error("the desk paired with a machine that refused")
			}
			if got := trustedIn(t, directory).Peers(); len(got) != 0 {
				t.Errorf("the command stored %+v", got)
			}
			if got := desk.store.Peers(); len(got) != 0 {
				t.Errorf("the desk stored %+v", got)
			}
		})
	}
}

func TestYesIsUnderstoodInAnyCaseAndWithSpacesAround(t *testing.T) {
	asked := pairing.Question{Name: "desk", OwnCode: "AAAA-AAAA-AAAA-AAAA", OtherCode: "BBBB-BBBB-BBBB-BBBB"}
	cases := map[string]bool{
		"yes\n":      true,
		"YES\n":      true,
		"  Yes \r\n": true,
		"yes":        true,
		"no\n":       false,
		"yess\n":     false,
		"\nyes\n":    false,
	}
	for typed, want := range cases {
		var shown bytes.Buffer
		if got := (terminal{stdin: strings.NewReader(typed), stdout: &shown}).Confirm(asked); got != want {
			t.Errorf("typing %q accepted = %v, want %v", typed, got, want)
		}
		swapped := `"This machine" must be BBBB-BBBB-BBBB-BBBB and "Other machine" must be AAAA-AAAA-AAAA-AAAA`
		if !strings.Contains(shown.String(), swapped) {
			t.Errorf("the question %q does not say %q", shown.String(), swapped)
		}
	}
}

func TestPairStoresNothingWhenTheOtherMachineRefuses(t *testing.T) {
	directory := useConfigDirectory(t)
	address, waited := newOtherMachine(t, "desk", false).waits(t)

	finished := await(t, startCommand("yes\n", &screen{}, "pair", address))
	await(t, waited)

	if finished.exitCode != 1 {
		t.Errorf("exit code = %d, stderr = %q, want 1", finished.exitCode, finished.stderr)
	}
	if got := trustedIn(t, directory).Peers(); len(got) != 0 {
		t.Errorf("the command stored %+v", got)
	}
}

func TestPairAsGivesTheOtherMachineANameWhenItsOwnIsTaken(t *testing.T) {
	directory := useConfigDirectory(t)
	oldDesk := trust.Peer{Name: "desk", Fingerprint: someFingerprint(1)}
	trustAlready(t, directory, oldDesk)
	desk := newOtherMachine(t, "desk", true)

	address, waited := desk.waits(t)
	finished := await(t, startCommand("yes\n", &screen{}, "pair", address))
	await(t, waited)

	if finished.exitCode != 1 || !strings.Contains(finished.stderr, "--as NAME") {
		t.Errorf("exit code = %d, stderr = %q, want 1 and a pointer to --as NAME", finished.exitCode, finished.stderr)
	}
	if got := trustedIn(t, directory).Peers(); !slices.Equal(got, []trust.Peer{oldDesk}) {
		t.Errorf("after the refusal the command trusts %+v, want only %+v", got, oldDesk)
	}

	address, waited = desk.waits(t)
	finished = await(t, startCommand("yes\n", &screen{}, "pair", "--as", "desk-2", address))
	await(t, waited)

	if finished.exitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %q", finished.exitCode, finished.stderr)
	}
	newDesk := trust.Peer{Name: "desk-2", Fingerprint: desk.identity.Fingerprint, Address: loopbackSharing}
	if got := trustedIn(t, directory).Peers(); !slices.Equal(got, []trust.Peer{oldDesk, newDesk}) {
		t.Errorf("the command trusts %+v, want %+v and %+v", got, oldDesk, newDesk)
	}
}

func TestPairFailsWhenNoMachineWaitsAtTheAddress(t *testing.T) {
	directory := useConfigDirectory(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	address := listener.Addr().String()
	listener.Close()

	finished := await(t, startCommand("yes\n", &screen{}, "pair", address))

	if finished.exitCode != 1 || !strings.Contains(finished.stderr, "cannot connect to "+address) {
		t.Errorf("exit code = %d, stderr = %q, want 1 and the address that failed", finished.exitCode, finished.stderr)
	}
	if got := trustedIn(t, directory).Peers(); len(got) != 0 {
		t.Errorf("the command stored %+v", got)
	}
}

func TestCommandsUsedWronglyShowUsageAndTouchNothing(t *testing.T) {
	cases := map[string][]string{
		"pair with two addresses":         {"pair", "192.168.0.10", "192.168.0.11"},
		"pair with a name trust refuses":  {"pair", "--as", "my desk", "192.168.0.10"},
		"pair that listens and dials":     {"pair", "--listen", "127.0.0.1:0", "192.168.0.10"},
		"pair with an unknown option":     {"pair", "--password", "1234"},
		"forget without a name":           {"forget"},
		"forget with more than one name":  {"forget", "laptop", "desk"},
		"pair with options after address": {"pair", "192.168.0.10", "--as", "desk"},
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

func TestAMachineForgottenWhilePairWaitsStaysForgotten(t *testing.T) {
	directory := useConfigDirectory(t)
	trustAlready(t, directory, trustedLaptop)
	tablet := newOtherMachine(t, "tablet", true)
	output := &screen{}

	command := startCommand("yes\n", output, "pair", "--listen", "127.0.0.1:0")
	port := output.portShown(t)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"forget", trustedLaptop.Name}, nobodyTyping(), &stdout, &stderr); code != 0 {
		t.Fatalf("forget: exit code = %d, stderr = %q", code, stderr.String())
	}
	_, err := pairing.Dial(net.JoinHostPort("127.0.0.1", port), tablet.config)
	finished := await(t, command)

	if err != nil || finished.exitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %q, the tablet got %v", finished.exitCode, finished.stderr, err)
	}
	tabletHere := trust.Peer{Name: "tablet", Fingerprint: tablet.identity.Fingerprint}
	if got := trustedIn(t, directory).Peers(); !slices.Equal(got, []trust.Peer{tabletHere}) {
		t.Errorf("the machine trusts %+v, want only %+v", got, tabletHere)
	}
}
