package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/identity"
	"github.com/rodrigogaraujo/lanhop/internal/pairing"
	"github.com/rodrigogaraujo/lanhop/internal/porttest"
	"github.com/rodrigogaraujo/lanhop/internal/sharer"
	"github.com/rodrigogaraujo/lanhop/internal/transport"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

var shortJoinLine = regexp.MustCompile(`lanhop join (\S+) (\S+)`)

type joiningMachine struct {
	identity *identity.Identity
	store    *trust.Store
}

func newJoiningMachine(t *testing.T) joiningMachine {
	t.Helper()
	directory := t.TempDir()
	return joiningMachine{identity: identityIn(t, directory), store: trustedIn(t, directory)}
}

func (s *screen) joinLineShown(t *testing.T) (string, pairing.JoinCode) {
	t.Helper()
	for deadline := time.Now().Add(testPatience); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if found := shortJoinLine.FindStringSubmatch(s.String()); found != nil {
			code, err := pairing.ParseJoinCode(found[2])
			if err != nil {
				t.Fatalf("lanhop add printed %q, which is not a join code: %v", found[2], err)
			}
			return found[1], code
		}
	}
	t.Fatalf("lanhop add never printed a join line, it printed %q", s.String())
	return "", pairing.JoinCode{}
}

type addRun struct {
	output   *screen
	finished chan error
	opened   chan string
	editor   *string
}

func runningAdd(t *testing.T, args ...string) addRun {
	t.Helper()
	run := addRun{output: &screen{}, finished: make(chan error, 1), opened: browserThat(t, nil), editor: new(string)}
	go func() {
		run.finished <- add(append([]string{"--listen", "127.0.0.1:0", "--address", "127.0.0.1"}, args...), run.output)
	}()
	return run
}

func (r addRun) editorAt(t *testing.T, path string) string {
	t.Helper()
	if *r.editor == "" {
		*r.editor = addressOpenedIn(t, r.opened)
	}
	opened, err := url.Parse(*r.editor)
	if err != nil {
		t.Fatalf("the editor opened at an address that cannot be read: %v", err)
	}
	opened.Path = path
	return opened.String()
}

func (r addRun) finishInTheEditor(t *testing.T) error {
	t.Helper()
	if status := post(t, r.editorAt(t, "/done"), struct{}{}); status != http.StatusOK {
		t.Errorf("POST /done: status %d, want %d", status, http.StatusOK)
	}
	return ended(t, r.finished)
}

func ended(t *testing.T, finished <-chan error) error {
	t.Helper()
	select {
	case err := <-finished:
		return err
	case <-time.After(2 * testPatience):
		t.Fatal("the command did not end")
		return nil
	}
}

func joinAs(t *testing.T, newcomer joiningMachine, output *screen) error {
	t.Helper()
	address, code := output.joinLineShown(t)
	_, err := pairing.Join(address, code, pairing.JoinConfig{Identity: newcomer.identity, Store: newcomer.store, Timeout: testPatience})
	return err
}

func TestJoinAddressesKeepPrivateAddressesOfRealInterfacesWithTheRouteFirst(t *testing.T) {
	interfaces := []networkInterface{
		{name: "lo0", up: true, loopback: true, addresses: []net.IP{net.ParseIP("127.0.0.1")}},
		{name: "en0", up: true, addresses: []net.IP{net.ParseIP("192.168.1.10"), net.ParseIP("fe80::1")}},
		{name: "utun3", up: true, addresses: []net.IP{net.ParseIP("10.8.0.2")}},
		{name: "docker0", up: true, addresses: []net.IP{net.ParseIP("172.17.0.1")}},
		{name: "bridge100", up: true, addresses: []net.IP{net.ParseIP("192.168.64.1")}},
		{name: "virbr0", up: true, addresses: []net.IP{net.ParseIP("192.168.122.1")}},
		{name: "en7", addresses: []net.IP{net.ParseIP("10.0.0.5")}},
		{name: "en1", up: true, addresses: []net.IP{net.ParseIP("8.8.8.8"), net.ParseIP("10.0.1.7")}},
	}
	got := joinAddresses(interfaces, net.ParseIP("10.0.1.7"))
	if want := []string{"10.0.1.7", "192.168.1.10"}; !slices.Equal(got, want) {
		t.Errorf("joinAddresses = %v, want %v", got, want)
	}
	if got := joinAddresses(interfaces, nil); !slices.Equal(got, []string{"192.168.1.10", "10.0.1.7"}) {
		t.Errorf("joinAddresses without a route = %v, want the interfaces in order", got)
	}
}

func TestTheJoinLinesNameTheReleaseOfTheCentral(t *testing.T) {
	code, err := pairing.NewJoinCode(someFingerprint(7))
	if err != nil {
		t.Fatalf("NewJoinCode: %v", err)
	}
	installer, short := joinLines("v0.1.2", "192.168.1.10", code)
	for _, part := range []string{
		"https://raw.githubusercontent.com/rodrigogaraujo/lanhop/v0.1.2/install.sh",
		"LANHOP_VERSION=v0.1.2",
		fmt.Sprintf(`LANHOP_JOIN="192.168.1.10 %s"`, code),
	} {
		if !strings.Contains(installer, part) {
			t.Errorf("installer line %q does not hold %q", installer, part)
		}
	}
	if want := "lanhop join 192.168.1.10 " + code.String(); short != want {
		t.Errorf("short line %q, want %q", short, want)
	}

	installer, _ = joinLines("dev-1234abc", "192.168.1.10", code)
	if !strings.Contains(installer, "/main/install.sh") || strings.Contains(installer, "LANHOP_VERSION") {
		t.Errorf("installer line of a build that is not a release = %q, want the installer of main and no version", installer)
	}
	if got := joinAddress("192.168.1.10", fmt.Sprint(pairing.DefaultPort)); got != "192.168.1.10" {
		t.Errorf("joinAddress on the pairing port = %q, want the host alone", got)
	}
	if got := joinAddress("192.168.1.10", "30000"); got != "192.168.1.10:30000" {
		t.Errorf("joinAddress on another port = %q, want the host and the port", got)
	}
	if got := joinAddress("192.168.1.10:31000", "30000"); got != "192.168.1.10:31000" {
		t.Errorf("joinAddress of an address given with its port = %q, want it as given", got)
	}
}

func TestAddPrintsAJoinLineAndStoresTheMachineThatJoins(t *testing.T) {
	central := useConfigDirectory(t)
	trustAlready(t, central, trust.Peer{Name: "server", Fingerprint: someFingerprint(1)})
	layoutPath := filepath.Join(central, layoutFileName)
	drawn := []byte(pairing.OwnName() + " server\n")
	if err := os.WriteFile(layoutPath, drawn, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	run := runningAdd(t, "office-mac")

	if err := joinAs(t, newJoiningMachine(t), run.output); err != nil {
		t.Fatalf("join: %v", err)
	}
	status, body := get(t, run.editorAt(t, "/desk"))
	var desk struct {
		Rows     [][]string `json:"rows"`
		Machines []string   `json:"machines"`
	}
	if err := json.Unmarshal([]byte(body), &desk); status != http.StatusOK || err != nil {
		t.Fatalf("GET /desk = %d %s, %v", status, body, err)
	}
	inARow := func(name string) bool {
		return slices.ContainsFunc(desk.Rows, func(row []string) bool { return slices.Contains(row, name) })
	}
	if !slices.Contains(desk.Machines, "office-mac") || inARow("office-mac") || !inARow("server") {
		t.Errorf("the editor shows %+v, want server on the desk and office-mac among the machines in no row, so in the tray", desk)
	}
	if err := run.finishInTheEditor(t); err != nil {
		t.Fatalf("add: %v", err)
	}
	if kept, err := os.ReadFile(layoutPath); err != nil || string(kept) != string(drawn) {
		t.Errorf("the layout file holds %q, %v after lanhop add, want it untouched: %q", kept, err, drawn)
	}
	shown := run.output.String()
	for _, part := range []string{"LANHOP_JOIN=", "office-mac joined from 127.0.0.1. Ctrl+Alt+3 moves to it.", "waits in the tray"} {
		if !strings.Contains(shown, part) {
			t.Errorf("lanhop add printed %q, want it to hold %q", shown, part)
		}
	}
	names := []string{}
	for _, peer := range trustedIn(t, central).Peers() {
		names = append(names, peer.Name)
	}
	if !slices.Equal(names, []string{"server", "office-mac"}) {
		t.Errorf("the central trusts %v, want server and office-mac", names)
	}
}

func TestAJoinStillCountsWhenTheLayoutEditorCannotOpen(t *testing.T) {
	central := useConfigDirectory(t)
	if err := os.MkdirAll(central, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(central, layoutFileName), []byte(pairing.OwnName()+" nobody-here\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	run := runningAdd(t, "office-mac")
	if err := joinAs(t, newJoiningMachine(t), run.output); err != nil {
		t.Fatalf("join: %v", err)
	}
	if err := ended(t, run.finished); err != nil {
		t.Fatalf("add with a layout file that has problems = %v, want the join to count", err)
	}
	expectNeverOpened(t, run.opened)
	if shown := run.output.String(); !strings.Contains(shown, "did not open") || !strings.Contains(shown, "Place office-mac on the desk later with: lanhop layout --edit") {
		t.Errorf("lanhop add printed %q, want it to say the editor did not open and how to place the machine later", shown)
	}
	names := []string{}
	for _, peer := range trustedIn(t, central).Peers() {
		names = append(names, peer.Name)
	}
	if !slices.Equal(names, []string{"office-mac"}) {
		t.Errorf("the central trusts %v, want office-mac", names)
	}
}

func TestAMachineAddedBeyondTheHotkeysIsToldItHasNone(t *testing.T) {
	central := useConfigDirectory(t)
	for position := 0; ; position++ {
		if _, reachable := sharer.HotkeyNumber(position); !reachable {
			break
		}
		trustAlready(t, central, trust.Peer{Name: fmt.Sprintf("side-%d", position), Fingerprint: someFingerprint(byte(position + 1))})
	}
	run := runningAdd(t, "ninth")
	if err := joinAs(t, newJoiningMachine(t), run.output); err != nil {
		t.Fatalf("join: %v", err)
	}
	if err := run.finishInTheEditor(t); err != nil {
		t.Fatalf("add: %v", err)
	}
	if !strings.Contains(run.output.String(), "It has no hotkey") {
		t.Errorf("lanhop add printed %q, want it to say the ninth machine has no hotkey", run.output.String())
	}
}

func TestAddRefusesANameItCannotUse(t *testing.T) {
	central := useConfigDirectory(t)
	trustAlready(t, central, trust.Peer{Name: "server", Fingerprint: someFingerprint(1)})
	for _, args := range [][]string{{}, {"office mac"}, {pairing.OwnName()}, {"a", "b"}} {
		var usage usageError
		if err := add(args, &screen{}); !errors.As(err, &usage) {
			t.Errorf("add %q = %v, want a usage error", args, err)
		}
	}
	if err := add([]string{"--listen", "127.0.0.1:0", "server"}, &screen{}); err == nil || !strings.Contains(err.Error(), "lanhop forget server") {
		t.Errorf("add with a taken name = %v, want the advice to forget it first", err)
	}
	for _, address := range []string{"x$(reboot)", "a b", "10.0.0.1;ls", `"`, "-h", "10.0.0.1:0", "10.0.0.1:99999"} {
		var usage usageError
		if err := add([]string{"--listen", "127.0.0.1:0", "--address", address, "office-mac"}, &screen{}); !errors.As(err, &usage) {
			t.Errorf("add --address %q = %v, want a usage error", address, err)
		}
	}
}

func TestAddAndPairSayThatAnotherPairingWaits(t *testing.T) {
	useConfigDirectory(t)
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer taken.Close()
	if err := add([]string{"--listen", taken.Addr().String(), "office-mac"}, &screen{}); !errors.Is(err, errAnotherPairingWaits) {
		t.Errorf("add on a taken port = %v, want errAnotherPairingWaits", err)
	}
	if err := pair([]string{"--listen", taken.Addr().String()}, strings.NewReader(""), &screen{}); !errors.Is(err, errAnotherPairingWaits) {
		t.Errorf("pair on a taken port = %v, want errAnotherPairingWaits", err)
	}
}

func TestAMachineAddedWhileShareListensConnectsWithoutARestart(t *testing.T) {
	directory := useConfigDirectory(t)
	central := identityIn(t, directory)
	listener, err := transport.Listen("127.0.0.1:0", transport.Config{Identity: central}, peersOnDisk(filepath.Join(directory, peersFileName)))
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer listener.Close()

	newcomer := newJoiningMachine(t)
	run := runningAdd(t, "office-mac")
	if err := joinAs(t, newcomer, run.output); err != nil {
		t.Fatalf("join: %v", err)
	}
	if err := run.finishInTheEditor(t); err != nil {
		t.Fatalf("add: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testPatience)
	defer cancel()
	conn, err := transport.Dial(ctx, transport.Config{Identity: newcomer.identity}, trust.Peer{Name: "imac", Fingerprint: central.Fingerprint, Address: listener.Addr().String()})
	if err != nil {
		t.Fatalf("the machine stored after the listener started could not connect: %v", err)
	}
	defer conn.Close()
	accepted, err := listener.Accept()
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	defer accepted.Close()
	if accepted.Peer().Name != "office-mac" {
		t.Errorf("the central took the connection as %q, want office-mac", accepted.Peer().Name)
	}
}

func waitingCentral(t *testing.T) (string, pairing.JoinCode, joiningMachine) {
	t.Helper()
	central := newJoiningMachine(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	code, err := pairing.NewJoinCode(central.identity.Fingerprint)
	if err != nil {
		t.Fatalf("NewJoinCode: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go pairing.WaitForJoin(ctx, listener, pairing.WaitConfig{Identity: central.identity, Store: central.store, OwnName: "imac", Name: "office-mac", Code: code})
	return listener.Addr().String(), code, central
}

func homeWithoutServices(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv(joinVariable, "")
	return home
}

func TestJoinStoresTheCentralAndSaysHowToStartLanhop(t *testing.T) {
	newcomer := useConfigDirectory(t)
	homeWithoutServices(t)
	address, code, central := waitingCentral(t)
	output := &screen{}

	if err := join([]string{address, code.String()}, output); err != nil {
		t.Fatalf("join: %v", err)
	}
	expectStoredCentral(t, newcomer, central)
	shown := output.String()
	if !strings.Contains(shown, `Joined "imac" at 127.0.0.1:24745.`) || !strings.Contains(shown, "LANHOP_ROLE=receive") {
		t.Errorf("lanhop join printed %q, want the central and the installer line of a side machine", shown)
	}
}

func TestJoinSaysTheServiceConnectsWhenOneReceives(t *testing.T) {
	useConfigDirectory(t)
	home := homeWithoutServices(t)
	unitDirectory := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(unitDirectory, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(unitDirectory, unitName), []byte("[Service]\nExecStart=\"/home/me/.local/bin/lanhop\" receive\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	address, code, _ := waitingCentral(t)
	output := &screen{}

	if err := join([]string{address, code.String()}, output); err != nil {
		t.Fatalf("join: %v", err)
	}
	if !strings.Contains(output.String(), "it connects to \"imac\" within about 10 seconds") {
		t.Errorf("lanhop join printed %q, want it to say the service connects", output.String())
	}
}

func TestJoinUnderTheInstallerSaysOnlyThatItJoined(t *testing.T) {
	useConfigDirectory(t)
	homeWithoutServices(t)
	address, code, _ := waitingCentral(t)
	t.Setenv(joinVariable, address+" "+code.String())
	output := &screen{}

	if err := join([]string{address, code.String()}, output); err != nil {
		t.Fatalf("join: %v", err)
	}
	if lines := strings.Count(output.String(), "\n"); lines != 1 {
		t.Errorf("lanhop join under the installer printed %q, want the one line that says it joined", output.String())
	}
}

func TestJoinWithNothingListeningLooksAtItsOwnStoreFirst(t *testing.T) {
	newcomer := useConfigDirectory(t)
	homeWithoutServices(t)
	central := newJoiningMachine(t)
	code, err := pairing.NewJoinCode(central.identity.Fingerprint)
	if err != nil {
		t.Fatalf("NewJoinCode: %v", err)
	}
	nobody := porttest.RefusingAddress(t)

	var usage usageError
	if err := join([]string{nobody, "not-a-code"}, &screen{}); !errors.As(err, &usage) {
		t.Errorf("join with a wrong code = %v, want a usage error", err)
	}
	if err := join([]string{nobody, code.String()}, &screen{}); err == nil || !strings.Contains(err.Error(), "lanhop add there again") {
		t.Errorf("join with nothing listening = %v, want the advice to run lanhop add again", err)
	}

	ownCode, err := pairing.NewJoinCode(identityIn(t, newcomer).Fingerprint)
	if err != nil {
		t.Fatalf("NewJoinCode: %v", err)
	}
	if err := join([]string{nobody, ownCode.String()}, &screen{}); err == nil || !strings.Contains(err.Error(), "central of this join code") {
		t.Errorf("join with the code of this machine = %v, want it to say this is the central", err)
	}

	trustAlready(t, newcomer, trust.Peer{Name: "imac", Fingerprint: central.identity.Fingerprint, Address: loopbackSharing})
	output := &screen{}
	if err := join([]string{nobody, code.String()}, output); err != nil {
		t.Errorf("join on a machine that already trusts the central = %v, want success", err)
	}
	if !strings.Contains(output.String(), "lanhop forget imac") {
		t.Errorf("lanhop join printed %q, want how to start over", output.String())
	}
	expectStoredCentral(t, newcomer, central)

	other := newJoiningMachine(t)
	otherCode, err := pairing.NewJoinCode(other.identity.Fingerprint)
	if err != nil {
		t.Fatalf("NewJoinCode: %v", err)
	}
	if err := join([]string{nobody, otherCode.String()}, &screen{}); err == nil || !strings.Contains(err.Error(), "lanhop forget imac") {
		t.Errorf("join of another central = %v, want the advice to forget imac first", err)
	}
}

func TestJoinSaysWhatToDoWhenTheCodeIsWrongOrTheCentralDoesNotAnswer(t *testing.T) {
	useConfigDirectory(t)
	homeWithoutServices(t)
	address, code, _ := waitingCentral(t)
	text := code.String()
	mistyped := text[:len(text)-4] + "AAAA"
	if mistyped == text {
		mistyped = text[:len(text)-4] + "BBBB"
	}
	if err := join([]string{address, mistyped}, &screen{}); err == nil || !strings.Contains(err.Error(), "Type the line again") {
		t.Errorf("join with a wrong token = %v, want the advice to type it again", err)
	}

	patience := joinPatience
	joinPatience = 200 * time.Millisecond
	t.Cleanup(func() { joinPatience = patience })
	if err := join([]string{"192.0.2.1", text}, &screen{}); err == nil || !strings.Contains(err.Error(), "VPN") {
		t.Errorf("join with an address that does not answer = %v, want it to mention a VPN", err)
	}
}

func expectStoredCentral(t *testing.T, directory string, central joiningMachine) {
	t.Helper()
	want := []trust.Peer{{Name: "imac", Fingerprint: central.identity.Fingerprint, Address: loopbackSharing}}
	if got := trustedIn(t, directory).Peers(); !slices.Equal(got, want) {
		t.Errorf("the new machine trusts %+v, want %+v", got, want)
	}
}
