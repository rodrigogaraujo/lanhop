package pairing

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/porttest"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

type rejection struct {
	address string
	reason  error
}

type rejections struct {
	mutex sync.Mutex
	seen  []rejection
}

func (r *rejections) note(address string, reason error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.seen = append(r.seen, rejection{address, reason})
}

func (r *rejections) count() int {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return len(r.seen)
}

func (r *rejections) because(reason error) []rejection {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return slices.DeleteFunc(slices.Clone(r.seen), func(seen rejection) bool { return !errors.Is(seen.reason, reason) })
}

type openWait struct {
	address  string
	code     JoinCode
	rejected *rejections
	finished <-chan outcome
	stop     context.CancelFunc
}

func waitingForJoin(t *testing.T, central machine, name string, adjust ...func(*WaitConfig)) openWait {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	code, err := NewJoinCode(central.identity.Fingerprint)
	if err != nil {
		t.Fatalf("NewJoinCode: %v", err)
	}
	rejected := &rejections{}
	config := WaitConfig{
		Identity:          central.identity,
		Store:             central.store,
		OwnName:           central.name,
		Name:              name,
		Code:              code,
		Rejected:          rejected.note,
		FirstMessageLimit: testPatience,
	}
	for _, change := range adjust {
		change(&config)
	}
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	finished := make(chan outcome, 1)
	go func() {
		joined, err := WaitForJoin(ctx, listener, config)
		finished <- outcome{joined.Peer, err}
	}()
	return openWait{address: listener.Addr().String(), code: code, rejected: rejected, finished: finished, stop: stop}
}

func (m machine) joinConfig() JoinConfig {
	return JoinConfig{Identity: m.identity, Store: m.store, Timeout: testPatience}
}

func joinsWith(t *testing.T, newcomer machine, address string, code JoinCode) outcome {
	t.Helper()
	peer, err := Join(address, code, newcomer.joinConfig())
	return outcome{peer, err}
}

func withToken(code JoinCode, token string) JoinCode {
	copy(code.token[:], token)
	return code
}

func expectStillWaiting(t *testing.T, wait openWait) {
	t.Helper()
	select {
	case got := <-wait.finished:
		t.Fatalf("the wait ended with %+v, want it to go on", got)
	case <-time.After(shortTimeout):
	}
}

func expectRefusedConnection(t *testing.T, address string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", address, testPatience)
	if err == nil {
		conn.Close()
		t.Fatalf("%s still takes connections", address)
	}
}

func TestAJoinCodeStoresEachMachineOnTheOther(t *testing.T) {
	central, newcomer := newMachine(t, "imac"), newMachine(t, "office")
	wait := waitingForJoin(t, central, "office-mac")

	joined := joinsWith(t, newcomer, wait.address, wait.code)
	waited := result(t, wait.finished)

	if joined.err != nil || waited.err != nil {
		t.Fatalf("join failed: new machine %v, central %v", joined.err, waited.err)
	}
	expectTrusted(t, central, trust.Peer{Name: "office-mac", Fingerprint: newcomer.identity.Fingerprint})
	expectTrusted(t, newcomer, trust.Peer{Name: "imac", Fingerprint: central.identity.Fingerprint, Address: loopbackSharing})
	if waited.peer.Name != "office-mac" || joined.peer.Name != "imac" {
		t.Errorf("central got %+v and the new machine got %+v", waited.peer, joined.peer)
	}
}

func TestAJoinCodeReadsBackWithoutDashesOrCaseAndStartsWithTheCodeOfTheCentral(t *testing.T) {
	central := newMachine(t, "imac")
	code, err := NewJoinCode(central.identity.Fingerprint)
	if err != nil {
		t.Fatalf("NewJoinCode: %v", err)
	}
	text := code.String()
	if len(text) != 29 || strings.Count(text, "-") != 5 {
		t.Fatalf("join code %q, want 24 characters in six groups of four", text)
	}
	if !strings.HasPrefix(text, central.code()+"-") {
		t.Errorf("join code %q does not start with the code of the central %q", text, central.code())
	}
	for _, typed := range []string{text, strings.ToLower(text), strings.ReplaceAll(text, "-", ""), " " + text + " "} {
		if read, err := ParseJoinCode(typed); err != nil || read != code {
			t.Errorf("ParseJoinCode(%q) = %v, %v, want the same code", typed, read, err)
		}
	}
	for _, wrong := range []string{"", text[:28], text + "A", strings.Replace(text, text[:1], "1", 1), central.code()} {
		if _, err := ParseJoinCode(wrong); !errors.Is(err, ErrNotAJoinCode) {
			t.Errorf("ParseJoinCode(%q) = %v, want ErrNotAJoinCode", wrong, err)
		}
	}
}

func TestACodeForAnotherCentralFailsTheHandshakeAndCostsNoAttempt(t *testing.T) {
	central, newcomer, other := newMachine(t, "imac"), newMachine(t, "office"), newMachine(t, "other")
	wait := waitingForJoin(t, central, "office-mac")
	forOther, err := NewJoinCode(other.identity.Fingerprint)
	if err != nil {
		t.Fatalf("NewJoinCode: %v", err)
	}
	forOther.token = wait.code.token

	for range JoinAttempts {
		if got := joinsWith(t, newcomer, wait.address, forOther); !errors.Is(got.err, ErrWrongCentral) {
			t.Fatalf("join with the code of another central = %v, want ErrWrongCentral", got.err)
		}
	}
	expectStillWaiting(t, wait)
	if wrong := wait.rejected.because(ErrWrongToken); len(wrong) != 0 {
		t.Errorf("the central counted %d wrong tokens, want none, because no token was sent", len(wrong))
	}
	expectTrusted(t, newcomer)
	if got := joinsWith(t, newcomer, wait.address, wait.code); got.err != nil {
		t.Fatalf("the right code after that: %v", got.err)
	}
}

func TestANameTheNewMachineCannotStoreIsRefusedBeforeTheTokenIsSent(t *testing.T) {
	central, newcomer, another := newMachine(t, "imac"), newMachine(t, "office"), newMachine(t, "spare")
	formerSide := trust.Peer{Name: "imac", Fingerprint: newMachine(t, "laptop").identity.Fingerprint}
	if err := newcomer.store.Add(formerSide); err != nil {
		t.Fatalf("Add: %v", err)
	}
	wait := waitingForJoin(t, central, "spare")

	if got := joinsWith(t, newcomer, wait.address, wait.code); !errors.Is(got.err, ErrNameTaken) || !strings.Contains(got.err.Error(), "lanhop forget imac") {
		t.Fatalf("join with the name of the central taken here = %v, want ErrNameTaken with the advice to forget it", got.err)
	}
	expectTrusted(t, newcomer, formerSide)
	if wrong := wait.rejected.because(ErrWrongToken); len(wrong) != 0 {
		t.Errorf("the central counted %d wrong tokens, want none", len(wrong))
	}
	if got := joinsWith(t, another, wait.address, wait.code); got.err != nil {
		t.Fatalf("the token after that: %v, want it still good", got.err)
	}
}

func TestThreeWrongTokensEndTheWaitAndEachIsReported(t *testing.T) {
	central := newMachine(t, "imac")
	wait := waitingForJoin(t, central, "office-mac")
	wrong := withToken(wait.code, "WRONG")

	for attempt := 1; attempt <= JoinAttempts; attempt++ {
		guesser := newMachine(t, "guesser")
		if got := joinsWith(t, guesser, wait.address, wrong); !errors.Is(got.err, ErrWrongToken) {
			t.Fatalf("wrong token %d = %v, want ErrWrongToken", attempt, got.err)
		}
		expectTrusted(t, guesser)
	}

	if got := result(t, wait.finished); !errors.Is(got.err, ErrTooManyAttempts) {
		t.Fatalf("the wait ended with %v, want ErrTooManyAttempts", got.err)
	}
	reported := wait.rejected.because(ErrWrongToken)
	if len(reported) != JoinAttempts {
		t.Fatalf("reported %d wrong tokens, want %d", len(reported), JoinAttempts)
	}
	for _, each := range reported {
		if each.address != "127.0.0.1" {
			t.Errorf("a wrong token was reported from %q, want 127.0.0.1", each.address)
		}
	}
	expectTrusted(t, central)
	expectRefusedConnection(t, wait.address)
}

func TestTheCentralRefusesItsOwnKey(t *testing.T) {
	central := newMachine(t, "imac")
	wait := waitingForJoin(t, central, "office-mac")
	itself := machine{name: "imac", identity: central.identity, store: newMachine(t, "empty").store}

	if _, err := exchange(wait.address, wait.code, itself.joinConfig()); !errors.Is(err, ErrSelf) {
		t.Fatalf("join with the key of the central = %v, want ErrSelf", err)
	}
	if got := result(t, wait.finished); !errors.Is(got.err, ErrSelf) {
		t.Errorf("the wait ended with %v, want ErrSelf", got.err)
	}
	expectTrusted(t, central)
}

func TestTheCentralRefusesAMachineItAlreadyTrusts(t *testing.T) {
	central, newcomer := newMachine(t, "imac"), newMachine(t, "office")
	known := trust.Peer{Name: "old-name", Fingerprint: newcomer.identity.Fingerprint}
	if err := central.store.Add(known); err != nil {
		t.Fatalf("Add: %v", err)
	}
	wait := waitingForJoin(t, central, "office-mac")

	if got := joinsWith(t, newcomer, wait.address, wait.code); !errors.Is(got.err, ErrKnownToCentral) {
		t.Fatalf("join of a machine the central trusts = %v, want ErrKnownToCentral", got.err)
	}
	if got := result(t, wait.finished); !errors.Is(got.err, ErrKnownToCentral) {
		t.Errorf("the wait ended with %v, want ErrKnownToCentral", got.err)
	}
	expectTrusted(t, central, known)
	expectTrusted(t, newcomer)
}

func TestTheCentralRefusesANameTakenDuringTheWait(t *testing.T) {
	central, newcomer := newMachine(t, "imac"), newMachine(t, "office")
	wait := waitingForJoin(t, central, "office-mac")
	elsewhere, err := trust.Open(central.storePath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	taken := trust.Peer{Name: "office-mac", Fingerprint: newMachine(t, "first").identity.Fingerprint}
	if err := elsewhere.Add(taken); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if got := joinsWith(t, newcomer, wait.address, wait.code); !errors.Is(got.err, ErrNameTaken) {
		t.Fatalf("join with a name taken during the wait = %v, want ErrNameTaken", got.err)
	}
	if got := result(t, wait.finished); !errors.Is(got.err, ErrNameTaken) {
		t.Errorf("the wait ended with %v, want ErrNameTaken", got.err)
	}
	expectTrusted(t, newcomer)
}

func TestStrayConnectionsCostNoAttemptAndDoNotDelayAJoin(t *testing.T) {
	central, newcomer := newMachine(t, "imac"), newMachine(t, "office")
	wait := waitingForJoin(t, central, "office-mac")

	closed, err := net.Dial("tcp", wait.address)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	closed.Close()
	garbage, err := net.Dial("tcp", wait.address)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer garbage.Close()
	garbage.Write([]byte("GET / HTTP/1.1\r\n\r\n"))
	silent, err := net.Dial("tcp", wait.address)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer silent.Close()
	stranger(t, wait.address, framed("not a join"))
	stranger(t, wait.address, []byte{2, tagJoin})
	halfHello, err := net.Dial("tcp", wait.address)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	halfHello.Write([]byte{0x16, 0x03, 0x01, 0x00, 0x80, 0x01})
	halfHello.Close()
	withoutCertificate, err := net.Dial("tcp", wait.address)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	unsigned := tls.Client(withoutCertificate, &tls.Config{MinVersion: tls.VersionTLS13, InsecureSkipVerify: true})
	unsigned.Handshake()
	unsigned.Close()

	started := time.Now()
	if got := joinsWith(t, newcomer, wait.address, wait.code); got.err != nil {
		t.Fatalf("join during stray connections: %v", got.err)
	}
	if took := time.Since(started); took > testPatience/2 {
		t.Errorf("the join took %v behind a silent connection, want it at once", took)
	}
	if got := result(t, wait.finished); got.err != nil {
		t.Fatalf("the wait ended with %v", got.err)
	}
	if wrong := wait.rejected.because(ErrWrongToken); len(wrong) != 0 {
		t.Errorf("stray connections counted %d wrong tokens, want none", len(wrong))
	}
	if closedAtTheEnd := wait.rejected.because(net.ErrClosed); len(closedAtTheEnd) != 0 {
		t.Errorf("%d connections the central closed at the end were reported, want none", len(closedAtTheEnd))
	}
}

func TestASilentConnectionIsClosedAtTheLimitAndReported(t *testing.T) {
	central := newMachine(t, "imac")
	wait := waitingForJoin(t, central, "office-mac", func(config *WaitConfig) { config.FirstMessageLimit = shortTimeout })

	silent, err := net.Dial("tcp", wait.address)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer silent.Close()
	silent.SetReadDeadline(time.Now().Add(testPatience))
	_, err = silent.Read(make([]byte, 1))
	var timeout net.Error
	if err == nil || (errors.As(err, &timeout) && timeout.Timeout()) {
		t.Fatalf("Read on a silent connection = %v, want the central to close it", err)
	}
	expectStillWaiting(t, wait)
	if wait.rejected.count() == 0 {
		t.Errorf("the silent connection was not reported")
	}
}

func TestANinthConnectionAtOnceIsRefusedAndTheWaitGoesOn(t *testing.T) {
	central, newcomer := newMachine(t, "imac"), newMachine(t, "office")
	wait := waitingForJoin(t, central, "office-mac")

	var held []net.Conn
	for range defaultJoinsAtOnce {
		conn, err := net.Dial("tcp", wait.address)
		if err != nil {
			t.Fatalf("Dial: %v", err)
		}
		held = append(held, conn)
	}
	deadline := time.Now().Add(testPatience)
	for len(wait.rejected.because(ErrTooManyAtOnce)) == 0 && time.Now().Before(deadline) {
		ninth, err := net.Dial("tcp", wait.address)
		if err != nil {
			t.Fatalf("Dial: %v", err)
		}
		ninth.SetReadDeadline(time.Now().Add(shortTimeout))
		ninth.Read(make([]byte, 1))
		ninth.Close()
	}
	if len(wait.rejected.because(ErrTooManyAtOnce)) == 0 {
		t.Fatalf("no connection was refused with %d already open", defaultJoinsAtOnce)
	}
	expectStillWaiting(t, wait)

	for _, conn := range held {
		conn.Close()
	}
	var joined outcome
	for time.Now().Before(deadline.Add(testPatience)) {
		if joined = joinsWith(t, newcomer, wait.address, wait.code); joined.err == nil {
			break
		}
	}
	if joined.err != nil {
		t.Fatalf("join after the connections closed: %v", joined.err)
	}
}

func TestTwoRightTokensAtOnceJoinExactlyOne(t *testing.T) {
	central := newMachine(t, "imac")
	first, second := newMachine(t, "first"), newMachine(t, "second")
	wait := waitingForJoin(t, central, "office-mac")

	outcomes := make(chan outcome, 2)
	for _, newcomer := range []machine{first, second} {
		go func() {
			peer, err := Join(wait.address, wait.code, newcomer.joinConfig())
			outcomes <- outcome{peer, err}
		}()
	}
	joined := 0
	for range 2 {
		got := result(t, outcomes)
		if got.err == nil {
			joined++
		}
		if errors.Is(got.err, ErrNameTaken) {
			t.Errorf("the second machine was refused for its name, want the spent token to stop it first")
		}
	}
	if joined != 1 {
		t.Fatalf("%d machines joined with one code, want exactly 1", joined)
	}
	if got := result(t, wait.finished); got.err != nil {
		t.Fatalf("the wait ended with %v", got.err)
	}
	if stored := central.trusted(t); len(stored) != 1 {
		t.Errorf("the central stored %+v, want one machine", stored)
	}
}

func TestAPairAgainstAJoinWaitFailsAtTheFirstMessageAndTheWaitGoesOn(t *testing.T) {
	central, pairing, newcomer := newMachine(t, "imac"), newMachine(t, "laptop"), newMachine(t, "office")
	wait := waitingForJoin(t, central, "office-mac")

	if _, err := Dial(wait.address, pairing.config(says(true))); !errors.Is(err, ErrMalformed) {
		t.Fatalf("lanhop pair against a join wait = %v, want ErrMalformed", err)
	}
	expectStillWaiting(t, wait)
	expectTrusted(t, pairing)
	if got := joinsWith(t, newcomer, wait.address, wait.code); got.err != nil {
		t.Fatalf("join after that: %v", got.err)
	}
}

func TestAJoinAgainstAPairWaitFailsAtTheFirstMessage(t *testing.T) {
	sharer, newcomer := newMachine(t, "sharer"), newMachine(t, "office")
	address, waited := waiting(t, sharer.config(says(true)))
	code, err := NewJoinCode(sharer.identity.Fingerprint)
	if err != nil {
		t.Fatalf("NewJoinCode: %v", err)
	}

	if got := joinsWith(t, newcomer, address, code); !errors.Is(got.err, ErrMalformed) {
		t.Fatalf("join against lanhop pair = %v, want ErrMalformed", got.err)
	}
	if got := result(t, waited); got.err == nil {
		t.Errorf("lanhop pair ended with nothing wrong, want it to end as on any stray connection")
	}
	expectTrusted(t, sharer)
	expectTrusted(t, newcomer)
}

func TestThePortTakesNoConnectionAfterAJoin(t *testing.T) {
	central, newcomer := newMachine(t, "imac"), newMachine(t, "office")
	wait := waitingForJoin(t, central, "office-mac")
	if got := joinsWith(t, newcomer, wait.address, wait.code); got.err != nil {
		t.Fatalf("join: %v", got.err)
	}
	result(t, wait.finished)
	expectRefusedConnection(t, wait.address)
}

func TestThePortTakesNoConnectionWhenTheWaitEnds(t *testing.T) {
	central, newcomer := newMachine(t, "imac"), newMachine(t, "office")
	wait := waitingForJoin(t, central, "office-mac")
	wait.stop()

	if got := result(t, wait.finished); !errors.Is(got.err, ErrWaitOver) {
		t.Fatalf("the wait ended with %v, want ErrWaitOver", got.err)
	}
	expectRefusedConnection(t, wait.address)
	if got := joinsWith(t, newcomer, wait.address, wait.code); !errors.Is(got.err, ErrNotWaiting) {
		t.Errorf("join after the wait = %v, want ErrNotWaiting", got.err)
	}
}

func TestAWaitWithANameThatCannotBeStoredDoesNotStart(t *testing.T) {
	central := newMachine(t, "imac")
	for _, names := range [][2]string{{"office mac", "imac"}, {"office-mac", ""}, {"office-mac", strings.Repeat("x", 300)}} {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("Listen: %v", err)
		}
		config := WaitConfig{Identity: central.identity, Store: central.store, Name: names[0], OwnName: names[1]}
		if _, err := WaitForJoin(context.Background(), listener, config); !errors.Is(err, ErrBadJoinName) {
			t.Errorf("WaitForJoin with names %q = %v, want ErrBadJoinName", names, err)
		}
		expectRefusedConnection(t, listener.Addr().String())
	}
}

func TestTheWaitEndsByItselfAtItsLimit(t *testing.T) {
	central := newMachine(t, "imac")
	wait := waitingForJoin(t, central, "office-mac", func(config *WaitConfig) { config.WaitLimit = shortTimeout })

	if got := result(t, wait.finished); !errors.Is(got.err, ErrWaitOver) {
		t.Fatalf("the wait ended with %v, want ErrWaitOver", got.err)
	}
	expectRefusedConnection(t, wait.address)
}

func TestAnAddressThatDoesNotAnswerIsUnreachable(t *testing.T) {
	central, newcomer := newMachine(t, "imac"), newMachine(t, "office")
	code, err := NewJoinCode(central.identity.Fingerprint)
	if err != nil {
		t.Fatalf("NewJoinCode: %v", err)
	}
	config := newcomer.joinConfig()
	config.Timeout = shortTimeout
	if _, err := Join("192.0.2.1", code, config); !errors.Is(err, ErrUnreachable) {
		t.Errorf("join with an address that does not answer = %v, want ErrUnreachable", err)
	}
}

func TestJoinLooksInItsOwnStoreBeforeConnecting(t *testing.T) {
	central := newMachine(t, "imac")
	code, err := NewJoinCode(central.identity.Fingerprint)
	if err != nil {
		t.Fatalf("NewJoinCode: %v", err)
	}
	nobody := porttest.RefusingAddress(t)

	joinedBefore := newMachine(t, "office")
	stored := trust.Peer{Name: "imac", Fingerprint: central.identity.Fingerprint, Address: loopbackSharing}
	if err := joinedBefore.store.Add(stored); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got := joinsWith(t, joinedBefore, nobody, code); !errors.Is(got.err, ErrAlreadyJoined) || got.peer != stored {
		t.Errorf("join on a machine that already trusts the central = %+v, %v, want ErrAlreadyJoined with it", got.peer, got.err)
	}

	elsewhere := newMachine(t, "laptop")
	if err := elsewhere.store.Add(trust.Peer{Name: "desk", Fingerprint: newMachine(t, "desk").identity.Fingerprint, Address: "192.0.2.1:24745"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got := joinsWith(t, elsewhere, nobody, code); !errors.Is(got.err, ErrOtherCentral) || !strings.Contains(got.err.Error(), "lanhop forget desk") {
		t.Errorf("join on a machine with another central = %v, want ErrOtherCentral with the advice to forget it", got.err)
	}

	if got := joinsWith(t, central, nobody, code); !errors.Is(got.err, ErrSelf) {
		t.Errorf("join typed on the central itself = %v, want ErrSelf", got.err)
	}

	formerCentral := newMachine(t, "old-desk")
	if err := formerCentral.store.Add(trust.Peer{Name: "side", Fingerprint: newMachine(t, "side").identity.Fingerprint}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got := joinsWith(t, formerCentral, nobody, code); !errors.Is(got.err, ErrNotWaiting) {
		t.Errorf("join on a former central with nothing listening = %v, want ErrNotWaiting", got.err)
	}
}
