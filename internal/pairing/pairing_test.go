package pairing

import (
	"crypto/tls"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/identity"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
	"github.com/rodrigogaraujo/lanhop/internal/wire"
)

const (
	testPatience    = 5 * time.Second
	shortTimeout    = 200 * time.Millisecond
	loopbackSharing = "127.0.0.1:24745"
)

type machine struct {
	name      string
	identity  *identity.Identity
	store     *trust.Store
	storePath string
}

func newMachine(t *testing.T, name string) machine {
	t.Helper()
	directory := t.TempDir()
	local, err := identity.LoadOrCreate(directory)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	storePath := filepath.Join(directory, "peers.json")
	store, err := trust.Open(storePath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return machine{name: name, identity: local, store: store, storePath: storePath}
}

func (m machine) config(asker Asker) Config {
	return Config{Identity: m.identity, Store: m.store, Name: m.name, Asker: asker, Timeout: testPatience}
}

func (m machine) code() string {
	return m.identity.Fingerprint.Code()
}

func (m machine) trusted(t *testing.T) []trust.Peer {
	t.Helper()
	saved, err := trust.Open(m.storePath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if inMemory := m.store.Peers(); !slices.Equal(inMemory, saved.Peers()) {
		t.Fatalf("%s holds %+v in memory and %+v on disk", m.name, inMemory, saved.Peers())
	}
	return saved.Peers()
}

func expectTrusted(t *testing.T, m machine, want ...trust.Peer) {
	t.Helper()
	if got := m.trusted(t); !slices.Equal(got, want) {
		t.Errorf("%s trusts %+v, want %+v", m.name, got, want)
	}
}

type person struct {
	accepts bool
	asked   chan Question
	leaves  chan struct{}
}

func says(accepts bool) *person {
	return &person{accepts: accepts, asked: make(chan Question, 1)}
}

func neverAnswers(t *testing.T) *person {
	t.Helper()
	absent := &person{asked: make(chan Question, 1), leaves: make(chan struct{})}
	t.Cleanup(func() { close(absent.leaves) })
	return absent
}

func (p *person) Confirm(question Question) bool {
	p.asked <- question
	if p.leaves != nil {
		<-p.leaves
	}
	return p.accepts
}

func (p *person) shown(t *testing.T) Question {
	t.Helper()
	select {
	case question := <-p.asked:
		return question
	case <-time.After(testPatience):
		t.Fatal("the person was never asked")
		return Question{}
	}
}

func (p *person) expectNeverAsked(t *testing.T) {
	t.Helper()
	select {
	case question := <-p.asked:
		t.Errorf("the person was asked %+v", question)
	default:
	}
}

type outcome struct {
	peer trust.Peer
	err  error
}

func waiting(t *testing.T, config Config) (string, <-chan outcome) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	finished := make(chan outcome, 1)
	go func() {
		peer, err := Accept(listener, config)
		finished <- outcome{peer, err}
	}()
	return listener.Addr().String(), finished
}

func joining(address string, config Config) <-chan outcome {
	finished := make(chan outcome, 1)
	go func() {
		peer, err := Dial(address, config)
		finished <- outcome{peer, err}
	}()
	return finished
}

func result(t *testing.T, finished <-chan outcome) outcome {
	t.Helper()
	select {
	case got := <-finished:
		return got
	case <-time.After(2 * testPatience):
		t.Fatal("pairing did not finish")
		return outcome{}
	}
}

func pair(t *testing.T, sharer, receiver Config) (outcome, outcome) {
	t.Helper()
	address, waited := waiting(t, sharer)
	joined := joining(address, receiver)
	return result(t, waited), result(t, joined)
}

func mustPair(t *testing.T, sharer, receiver machine) {
	t.Helper()
	onSharer, onReceiver := pair(t, sharer.config(says(true)), receiver.config(says(true)))
	if onSharer.err != nil || onReceiver.err != nil {
		t.Fatalf("pairing failed: sharer %v, receiver %v", onSharer.err, onReceiver.err)
	}
}

func framed(payload string) []byte {
	return append([]byte{byte(len(payload))}, payload...)
}

func strangerWith(t *testing.T, address string, config *tls.Config) *tls.Conn {
	t.Helper()
	conn, err := tls.Dial("tcp", address, config)
	if err != nil {
		t.Fatalf("tls.Dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func stranger(t *testing.T, address string, sent ...[]byte) *tls.Conn {
	t.Helper()
	conn := strangerWith(t, address, anyCertificate(newMachine(t, "stranger").identity))
	if _, err := conn.Write(slices.Concat(sent...)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return conn
}

func TestBothAcceptAndEachMachineStoresTheOther(t *testing.T) {
	desk, laptop := newMachine(t, "desk"), newMachine(t, "laptop")

	onDesk, onLaptop := pair(t, desk.config(says(true)), laptop.config(says(true)))

	if onDesk.err != nil || onLaptop.err != nil {
		t.Fatalf("pairing failed: desk %v, laptop %v", onDesk.err, onLaptop.err)
	}
	laptopOnDesk := trust.Peer{Name: "laptop", Fingerprint: laptop.identity.Fingerprint}
	deskOnLaptop := trust.Peer{Name: "desk", Fingerprint: desk.identity.Fingerprint, Address: loopbackSharing}
	if onDesk.peer != laptopOnDesk {
		t.Errorf("the sharer paired with %+v, want %+v", onDesk.peer, laptopOnDesk)
	}
	if onLaptop.peer != deskOnLaptop {
		t.Errorf("the receiver paired with %+v, want %+v", onLaptop.peer, deskOnLaptop)
	}
	expectTrusted(t, desk, laptopOnDesk)
	expectTrusted(t, laptop, deskOnLaptop)
}

func TestEachMachineShowsItsOwnCodeAndTheCodeOfTheOther(t *testing.T) {
	desk, laptop := newMachine(t, "desk"), newMachine(t, "laptop")
	atDesk, atLaptop := says(true), says(true)

	pair(t, desk.config(atDesk), laptop.config(atLaptop))

	if got, want := atDesk.shown(t), (Question{Name: "laptop", OwnCode: desk.code(), OtherCode: laptop.code()}); got != want {
		t.Errorf("the sharer showed %+v, want %+v", got, want)
	}
	if got, want := atLaptop.shown(t), (Question{Name: "desk", OwnCode: laptop.code(), OtherCode: desk.code()}); got != want {
		t.Errorf("the receiver showed %+v, want %+v", got, want)
	}
}

func TestARefusalOnEitherSideStoresNothingOnBoth(t *testing.T) {
	cases := map[string]struct{ deskAccepts, laptopAccepts bool }{
		"the sharer refuses":   {deskAccepts: false, laptopAccepts: true},
		"the receiver refuses": {deskAccepts: true, laptopAccepts: false},
		"both refuse":          {deskAccepts: false, laptopAccepts: false},
	}
	for name, answers := range cases {
		t.Run(name, func(t *testing.T) {
			desk, laptop := newMachine(t, "desk"), newMachine(t, "laptop")

			onDesk, onLaptop := pair(t, desk.config(says(answers.deskAccepts)), laptop.config(says(answers.laptopAccepts)))

			if onDesk.err == nil || onLaptop.err == nil {
				t.Errorf("pairing succeeded after a refusal: desk %v, laptop %v", onDesk.err, onLaptop.err)
			}
			if answers.laptopAccepts && !errors.Is(onDesk.err, ErrDeclined) {
				t.Errorf("the sharer got %v, want ErrDeclined", onDesk.err)
			}
			if answers.deskAccepts && !errors.Is(onLaptop.err, ErrDeclined) {
				t.Errorf("the receiver got %v, want ErrDeclined", onLaptop.err)
			}
			expectTrusted(t, desk)
			expectTrusted(t, laptop)
		})
	}
}

func TestARefusalEndsThePairingWhileTheOtherPersonIsStillDeciding(t *testing.T) {
	desk, laptop := newMachine(t, "desk"), newMachine(t, "laptop")

	onDesk, onLaptop := pair(t, desk.config(neverAnswers(t)), laptop.config(says(false)))

	if !errors.Is(onDesk.err, ErrPeerDeclined) {
		t.Errorf("the sharer got %v, want ErrPeerDeclined", onDesk.err)
	}
	if !errors.Is(onLaptop.err, ErrDeclined) {
		t.Errorf("the receiver got %v, want ErrDeclined", onLaptop.err)
	}
	expectTrusted(t, desk)
	expectTrusted(t, laptop)
}

func TestNothingIsStoredWhenTheOtherMachineDisconnectsAfterThisOneAccepted(t *testing.T) {
	desk := newMachine(t, "desk")
	atDesk := says(true)
	address, waited := waiting(t, desk.config(atDesk))

	leaver := stranger(t, address, framed("laptop"))
	atDesk.shown(t)
	leaver.Close()

	if got := result(t, waited); got.err == nil || errors.Is(got.err, ErrTimeout) {
		t.Errorf("Accept = %v, want an error that reports the lost connection", got.err)
	}
	expectTrusted(t, desk)
}

func TestAYesStopsCountingWhenTheMachineThatSentItLeaves(t *testing.T) {
	desk := newMachine(t, "desk")
	atDesk := neverAnswers(t)
	address, waited := waiting(t, desk.config(atDesk))

	leaver := stranger(t, address, framed("laptop"), framed(string(answerYes)))
	atDesk.shown(t)
	leaver.Close()

	if got := result(t, waited); got.err == nil || errors.Is(got.err, ErrTimeout) {
		t.Errorf("Accept = %v, want an error that reports the lost connection", got.err)
	}
	expectTrusted(t, desk)
}

func TestPairingTimesOutWhenThePersonNeverAnswers(t *testing.T) {
	desk, laptop := newMachine(t, "desk"), newMachine(t, "laptop")
	unattended := desk.config(neverAnswers(t))
	unattended.Timeout = shortTimeout

	onDesk, onLaptop := pair(t, unattended, laptop.config(says(true)))

	if !errors.Is(onDesk.err, ErrTimeout) {
		t.Errorf("the unattended sharer got %v, want ErrTimeout", onDesk.err)
	}
	if onLaptop.err == nil {
		t.Error("the receiver paired with a sharer that never answered")
	}
	expectTrusted(t, desk)
	expectTrusted(t, laptop)
}

func TestPairingTimesOutWhenTheOtherMachineNeverAnswers(t *testing.T) {
	desk, laptop := newMachine(t, "desk"), newMachine(t, "laptop")
	impatient := laptop.config(says(true))
	impatient.Timeout = shortTimeout

	onDesk, onLaptop := pair(t, desk.config(neverAnswers(t)), impatient)

	if !errors.Is(onLaptop.err, ErrTimeout) {
		t.Errorf("the receiver that accepted got %v, want ErrTimeout", onLaptop.err)
	}
	if onDesk.err == nil || errors.Is(onDesk.err, ErrTimeout) {
		t.Errorf("the sharer got %v, want an error that reports the lost connection", onDesk.err)
	}
	expectTrusted(t, desk)
	expectTrusted(t, laptop)
}

func TestAMachineInTheMiddleCannotMakeTheCodesMatch(t *testing.T) {
	desk, laptop := newMachine(t, "desk"), newMachine(t, "laptop")
	intruderAsLaptop, intruderAsDesk := newMachine(t, "laptop"), newMachine(t, "desk")
	intruderAsDesk.identity = intruderAsLaptop.identity
	atDesk, atLaptop := says(false), says(false)

	deskAddress, onDesk := waiting(t, desk.config(atDesk))
	intruderAddress, onIntruderAsDesk := waiting(t, intruderAsDesk.config(says(true)))
	onIntruderAsLaptop := joining(deskAddress, intruderAsLaptop.config(says(true)))
	onLaptop := joining(intruderAddress, laptop.config(atLaptop))

	shownAtDesk, shownAtLaptop := atDesk.shown(t), atLaptop.shown(t)
	if shownAtDesk.OwnCode != desk.code() || shownAtLaptop.OwnCode != laptop.code() {
		t.Fatalf("own codes shown: desk %s, laptop %s", shownAtDesk.OwnCode, shownAtLaptop.OwnCode)
	}
	if shownAtDesk.OtherCode == shownAtLaptop.OwnCode {
		t.Errorf("the sharer shows %s for the other machine, which is the receiver's own code", shownAtDesk.OtherCode)
	}
	if shownAtLaptop.OtherCode == shownAtDesk.OwnCode {
		t.Errorf("the receiver shows %s for the other machine, which is the sharer's own code", shownAtLaptop.OtherCode)
	}
	intruderCode := intruderAsLaptop.code()
	if shownAtDesk.OtherCode != intruderCode || shownAtLaptop.OtherCode != intruderCode {
		t.Errorf("codes shown for the other machine: desk %s, laptop %s, want the intruder's %s on both",
			shownAtDesk.OtherCode, shownAtLaptop.OtherCode, intruderCode)
	}

	for _, finished := range []<-chan outcome{onDesk, onLaptop, onIntruderAsDesk, onIntruderAsLaptop} {
		if got := result(t, finished); got.err == nil {
			t.Errorf("a session through the intruder paired with %+v", got.peer)
		}
	}
	for _, m := range []machine{desk, laptop, intruderAsDesk, intruderAsLaptop} {
		expectTrusted(t, m)
	}
}

func TestPairingAgainWithATrustedMachineIsRefused(t *testing.T) {
	desk, laptop := newMachine(t, "desk"), newMachine(t, "laptop")
	mustPair(t, desk, laptop)
	trustedByDesk, trustedByLaptop := desk.trusted(t), laptop.trusted(t)
	atDesk, atLaptop := says(true), says(true)

	onDesk, onLaptop := pair(t, desk.config(atDesk), laptop.config(atLaptop))

	if !errors.Is(onDesk.err, ErrAlreadyPaired) || !strings.Contains(onDesk.err.Error(), "lanhop forget laptop") {
		t.Errorf("the sharer got %v, want ErrAlreadyPaired with the command that forgets laptop", onDesk.err)
	}
	if !errors.Is(onLaptop.err, ErrAlreadyPaired) || !strings.Contains(onLaptop.err.Error(), "lanhop forget desk") {
		t.Errorf("the receiver got %v, want ErrAlreadyPaired with the command that forgets desk", onLaptop.err)
	}
	atDesk.expectNeverAsked(t)
	atLaptop.expectNeverAsked(t)
	expectTrusted(t, desk, trustedByDesk...)
	expectTrusted(t, laptop, trustedByLaptop...)
}

func TestAMachineTrustedOnOneSideOnlyIsRefusedAndStoresNothing(t *testing.T) {
	desk, laptop := newMachine(t, "desk"), newMachine(t, "laptop")
	remembered := trust.Peer{Name: "old-laptop", Fingerprint: laptop.identity.Fingerprint}
	if err := desk.store.Add(remembered); err != nil {
		t.Fatalf("Add: %v", err)
	}

	onDesk, onLaptop := pair(t, desk.config(says(true)), laptop.config(neverAnswers(t)))

	if !errors.Is(onDesk.err, ErrAlreadyPaired) || !strings.Contains(onDesk.err.Error(), "lanhop forget old-laptop") {
		t.Errorf("the sharer got %v, want ErrAlreadyPaired with the command that forgets old-laptop", onDesk.err)
	}
	if !errors.Is(onLaptop.err, ErrPeerDeclined) {
		t.Errorf("the receiver got %v, want ErrPeerDeclined", onLaptop.err)
	}
	expectTrusted(t, desk, remembered)
	expectTrusted(t, laptop)
}

func TestANameAlreadyInUseIsRefusedUntilAnotherIsGiven(t *testing.T) {
	desk := newMachine(t, "desk")
	first, second := newMachine(t, "laptop"), newMachine(t, "laptop")
	mustPair(t, desk, first)
	firstOnDesk := trust.Peer{Name: "laptop", Fingerprint: first.identity.Fingerprint}
	atDesk := says(true)

	onDesk, onSecond := pair(t, desk.config(atDesk), second.config(neverAnswers(t)))

	if !errors.Is(onDesk.err, ErrNameTaken) || !strings.Contains(onDesk.err.Error(), "--as NAME") {
		t.Errorf("the sharer got %v, want ErrNameTaken that points to --as NAME", onDesk.err)
	}
	if !errors.Is(onSecond.err, ErrPeerDeclined) {
		t.Errorf("the second laptop got %v, want ErrPeerDeclined", onSecond.err)
	}
	atDesk.expectNeverAsked(t)
	expectTrusted(t, desk, firstOnDesk)
	expectTrusted(t, second)

	renaming := desk.config(says(true))
	renaming.As = "laptop-2"
	onDesk, onSecond = pair(t, renaming, second.config(says(true)))

	if onDesk.err != nil || onSecond.err != nil {
		t.Fatalf("pairing under another name failed: desk %v, second laptop %v", onDesk.err, onSecond.err)
	}
	expectTrusted(t, desk, firstOnDesk, trust.Peer{Name: "laptop-2", Fingerprint: second.identity.Fingerprint})
	expectTrusted(t, second, trust.Peer{Name: "desk", Fingerprint: desk.identity.Fingerprint, Address: loopbackSharing})
}

func TestAMachineDoesNotPairWithItself(t *testing.T) {
	desk, mirror := newMachine(t, "desk"), newMachine(t, "mirror")
	mirror.identity = desk.identity

	onDesk, onMirror := pair(t, desk.config(says(true)), mirror.config(says(true)))

	if !errors.Is(onDesk.err, ErrSelf) || !errors.Is(onMirror.err, ErrSelf) {
		t.Errorf("desk got %v and its mirror got %v, want ErrSelf on both", onDesk.err, onMirror.err)
	}
	expectTrusted(t, desk)
	expectTrusted(t, mirror)
}

func TestMessagesThatAreNotPairingMessagesAreRefused(t *testing.T) {
	name := framed("laptop")
	cases := map[string]struct {
		sent        [][]byte
		askedAnyway bool
	}{
		"a name one byte too long":           {sent: [][]byte{framed(strings.Repeat("a", trust.MaxNameLength+1))}},
		"a name of 255 bytes":                {sent: [][]byte{framed(strings.Repeat("a", 255))}},
		"an empty name":                      {sent: [][]byte{{0}}},
		"a name with a space":                {sent: [][]byte{framed("my laptop")}},
		"a name that is a path":              {sent: [][]byte{framed("../laptop")}},
		"a name with an escape sequence":     {sent: [][]byte{framed("lap\x1b[2Jtop")}},
		"an answer of two bytes":             {sent: [][]byte{name, {2, answerYes, answerYes}}, askedAnyway: true},
		"an empty answer":                    {sent: [][]byte{name, {0}}, askedAnyway: true},
		"an answer that is not yes or no":    {sent: [][]byte{name, {1, 7}}, askedAnyway: true},
		"more bytes after the answer":        {sent: [][]byte{name, {1, answerYes}, {1, answerYes}}, askedAnyway: true},
		"the greeting of the input protocol": {sent: [][]byte{wire.Append(nil, event.Hello())}},
	}
	for label, test := range cases {
		t.Run(label, func(t *testing.T) {
			desk := newMachine(t, "desk")
			atDesk := neverAnswers(t)
			address, waited := waiting(t, desk.config(atDesk))

			stranger(t, address, test.sent...)

			if got := result(t, waited); !errors.Is(got.err, ErrMalformed) {
				t.Errorf("Accept = %v, want ErrMalformed", got.err)
			}
			if !test.askedAnyway {
				atDesk.expectNeverAsked(t)
			}
			expectTrusted(t, desk)
		})
	}
}

func TestTheReceiverRefusesMalformedMessagesToo(t *testing.T) {
	laptop := newMachine(t, "laptop")
	atLaptop := neverAnswers(t)
	listener, err := tls.Listen("tcp", "127.0.0.1:0", anyCertificate(newMachine(t, "stranger").identity))
	if err != nil {
		t.Fatalf("tls.Listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.Write(framed(strings.Repeat("a", trust.MaxNameLength+1)))
		conn.Read(make([]byte, 1+trust.MaxNameLength))
	}()

	got := result(t, joining(listener.Addr().String(), laptop.config(atLaptop)))

	if !errors.Is(got.err, ErrMalformed) {
		t.Errorf("Dial = %v, want ErrMalformed", got.err)
	}
	atLaptop.expectNeverAsked(t)
	expectTrusted(t, laptop)
}

func TestConnectionsThatAreNotMutualTLS13AreRefused(t *testing.T) {
	cases := map[string]func(*tls.Config){
		"an older TLS version": func(config *tls.Config) {
			config.MinVersion = tls.VersionTLS12
			config.MaxVersion = tls.VersionTLS12
		},
		"no certificate": func(config *tls.Config) { config.Certificates = nil },
	}
	for label, weaken := range cases {
		t.Run(label, func(t *testing.T) {
			desk := newMachine(t, "desk")
			atDesk := says(true)
			address, waited := waiting(t, desk.config(atDesk))
			config := anyCertificate(newMachine(t, "stranger").identity)
			weaken(config)

			if conn, err := tls.Dial("tcp", address, config); err == nil {
				conn.Write(framed("laptop"))
				conn.Write(framed(string(answerYes)))
				t.Cleanup(func() { conn.Close() })
			}

			if got := result(t, waited); got.err == nil {
				t.Errorf("Accept paired with %+v", got.peer)
			}
			atDesk.expectNeverAsked(t)
			expectTrusted(t, desk)
		})
	}
}

func TestAnAddressWithoutAPortUsesThePairingPort(t *testing.T) {
	cases := map[string]struct{ host, port string }{
		"192.168.0.10":      {"192.168.0.10", "24746"},
		"192.168.0.10:9000": {"192.168.0.10", "9000"},
		"desk.local":        {"desk.local", "24746"},
		"desk.local:9000":   {"desk.local", "9000"},
		"fe80::1":           {"fe80::1", "24746"},
		"[fe80::1]":         {"fe80::1", "24746"},
		"[fe80::1]:9000":    {"fe80::1", "9000"},
	}
	for address, want := range cases {
		if host, port := splitAddress(address); host != want.host || port != want.port {
			t.Errorf("splitAddress(%q) = %q, %q, want %q, %q", address, host, port, want.host, want.port)
		}
	}
}

func TestProposedNameIsTheHostnameReducedToWhatTrustAccepts(t *testing.T) {
	cases := map[string]string{
		"desk":                     "desk",
		"Marias-MacBook-Pro.local": "Marias-MacBook-Pro",
		"build_box-2.lan.example":  "build_box-2",
		"my desk!":                 "mydesk",
		strings.Repeat("a", 40):    strings.Repeat("a", 32),
		"café":                     "caf",
		"":                         "machine",
		".local":                   "machine",
		"日本":                       "machine",
	}
	for hostname, want := range cases {
		got := NameFrom(hostname)
		if got != want {
			t.Errorf("NameFrom(%q) = %q, want %q", hostname, got, want)
		}
		if !trust.ValidName(got) {
			t.Errorf("NameFrom(%q) = %q, which trust refuses", hostname, got)
		}
	}
}

func TestOwnNameIsTheNameProposedFromTheHostnameOfThisMachine(t *testing.T) {
	hostname, err := os.Hostname()
	if err != nil {
		t.Skipf("this machine does not tell its hostname: %v", err)
	}

	got := OwnName()

	if want := NameFrom(hostname); got != want {
		t.Errorf("OwnName() = %q, want %q, the name proposed from the hostname %q", got, want, hostname)
	}
	if !trust.ValidName(got) {
		t.Errorf("OwnName() = %q, which trust refuses", got)
	}
}
