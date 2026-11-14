package clipshare

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/filestream"
)

const shortDataSilence = time.Second

func transfersOnTheCentral(t *testing.T, c *Central) int {
	t.Helper()
	answer := make(chan int, 1)
	c.tell(func() { answer <- len(c.transfers) })
	select {
	case count := <-answer:
		return count
	case <-time.After(testPatience):
		t.Fatal("the central runtime did not answer")
		return -1
	}
}

func transfersOnTheSide(t *testing.T, s *Side) int {
	t.Helper()
	answer := make(chan int, 1)
	s.inbox.put(func() { answer <- len(s.transfers) })
	select {
	case count := <-answer:
		return count
	case <-time.After(testPatience):
		t.Fatal("the side machine runtime did not answer")
		return -1
	}
}

func (d *deskUnderTest) expectNoTransfersLeft(sides ...*sideUnderTest) {
	d.t.Helper()
	eventually(d.t, "the central forgetting every transfer", func() bool { return transfersOnTheCentral(d.t, d.runtime) == 0 })
	for _, side := range sides {
		eventually(d.t, side.name+" forgetting every transfer", func() bool { return transfersOnTheSide(d.t, side.runtime) == 0 })
	}
}

func newShortSilenceFileDesk(t *testing.T, sideNames ...string) *deskUnderTest {
	t.Helper()
	board := holding("what the central held before")
	return newDeskWith(t, deskSetup{board: board, open: opening(takingFiles{board}), files: true, dataSilence: shortDataSilence}, sideNames...)
}

func TestFocusThatLeavesInTheMiddleOfARelayLeavesNothingBehindAndTheNextVisitStartsItAgain(t *testing.T) {
	d := newSlowFileDesk(t, oneMebibytePerSecond, "left", "right")
	left, right := d.sideJoins("left"), d.sideJoins("right")
	d.waitUntilSharingFiles(left, right)
	d.focusOn(left.number)
	large := writeFile(t, filepath.Join(t.TempDir(), "video.mp4"), randomContent(t, 3<<20))

	left.clipboard.personCopiesFiles(large)
	d.focusOn(right.number)
	waitUntilArriving(t, "right", right.arrivalDir)
	d.focusOn(left.number)

	expectNothingLeftIn(t, "right", right.arrivalDir)
	time.Sleep(settling)
	expectNoFilesWritten(t, "right, which focus left in the middle", right.clipboard)
	d.expectNoTransfersLeft(left, right)
	d.focusOn(right.number)
	arrived := waitUntilFilesWritten(t, "right, at the next visit", right.clipboard, 1)
	expectSameFiles(t, arrived, right.arrivalDir, large)
}

func TestAFileThatChangesWhileItIsRelayedArrivesNowhere(t *testing.T) {
	d := newSlowFileDesk(t, oneMebibytePerSecond, "left", "right")
	left, right := d.sideJoins("left"), d.sideJoins("right")
	d.waitUntilSharingFiles(left, right)
	d.focusOn(left.number)
	log := writeFile(t, filepath.Join(t.TempDir(), "server.log"), randomContent(t, 3<<20))

	left.clipboard.personCopiesFiles(log)
	d.focusOn(right.number)
	waitUntilArriving(t, "right", right.arrivalDir)
	appendTo(t, log, "one more line\n")

	left.log.waitFor(t, filestream.ErrChanged.Error())
	d.log.waitFor(t, "The file copy from left to right stopped")
	expectNothingLeftIn(t, "right", right.arrivalDir)
	time.Sleep(settling)
	expectNoFilesWritten(t, "right", right.clipboard)
	d.expectNoTransfersLeft(right)
}

func TestARelayToAMachineWithoutRoomIsNotTakenAndTheLogSaysWhy(t *testing.T) {
	d := newFileDesk(t, "left", "right")
	left, right := d.sides["left"], d.sides["right"]
	right.arrivalDir, right.arrivals = openArrivals(t, 1<<20)
	d.sideJoins("left")
	d.sideJoins("right")
	d.waitUntilSharingFiles(left, right)
	d.focusOn(left.number)

	left.clipboard.personCopiesFiles(writeFile(t, filepath.Join(t.TempDir(), "too large"), randomContent(t, 2<<20)))
	d.focusOn(right.number)

	right.log.waitFor(t, "do not fit in the room lanhop allows itself")
	d.log.waitFor(t, "The file copy from left to right stopped")
	expectNothingLeftIn(t, "right", right.arrivalDir)
	d.expectNoTransfersLeft(left)
	if strings.Contains(left.log.String(), "use of closed network connection") {
		t.Errorf("left logged a closed connection instead of why the copy stopped:\n%s", left.log)
	}
}

func TestFocusThatLeavesTheCentralInTheMiddleOfAFetchLeavesNothingBehind(t *testing.T) {
	d := newSlowFileDesk(t, oneMebibytePerSecond, "left")
	left := d.sideJoins("left")
	d.waitUntilSharingFiles(left)
	d.focusOn(left.number)
	large := writeFile(t, filepath.Join(t.TempDir(), "big.bin"), randomContent(t, 3<<20))

	left.clipboard.personCopiesFiles(large)
	d.focusOn(thisMachine)
	waitUntilArriving(t, "the central", d.arrivalDir)
	d.focusOn(left.number)

	expectNothingLeftIn(t, "the central", d.arrivalDir)
	time.Sleep(settling)
	expectNoFilesWritten(t, "the central, which focus left in the middle", d.clipboard)
	d.expectNoTransfersLeft(left)
	d.focusOn(thisMachine)
	arrived := waitUntilFilesWritten(t, "the central, at the next visit", d.clipboard, 1)
	expectSameFiles(t, arrived, d.arrivalDir, large)
}

func TestAFileThatChangesWhileTheCentralFetchesItArrivesNowhere(t *testing.T) {
	d := newSlowFileDesk(t, oneMebibytePerSecond, "left")
	left := d.sideJoins("left")
	d.waitUntilSharingFiles(left)
	d.focusOn(left.number)
	log := writeFile(t, filepath.Join(t.TempDir(), "server.log"), randomContent(t, 3<<20))

	left.clipboard.personCopiesFiles(log)
	d.focusOn(thisMachine)
	waitUntilArriving(t, "the central", d.arrivalDir)
	appendTo(t, log, "one more line\n")

	left.log.waitFor(t, filestream.ErrChanged.Error())
	d.log.waitFor(t, "The file copy from left stopped")
	expectNothingLeftIn(t, "the central", d.arrivalDir)
	expectNoFilesWritten(t, "the central", d.clipboard)
}

func TestALostControlConnectionOfTheSendingMachineLeavesNothingOfARelayBehind(t *testing.T) {
	d := newSlowFileDesk(t, oneMebibytePerSecond, "left", "right")
	left := d.sides["left"]
	control := cuttableProxyTo(t, d.address)
	d.startSide(left, control.address, d.clipboardOpener(left.clipboard))
	right := d.sideJoins("right")
	d.waitUntilSharingFiles(left, right)
	d.focusOn(left.number)
	large := writeFile(t, filepath.Join(t.TempDir(), "disk.img"), randomContent(t, 3<<20))

	left.clipboard.personCopiesFiles(large)
	d.focusOn(right.number)
	waitUntilArriving(t, "right", right.arrivalDir)
	control.cut()

	expectNothingLeftIn(t, "right", right.arrivalDir)
	time.Sleep(settling)
	expectNoFilesWritten(t, "right", right.clipboard)
	d.expectNoTransfersLeft(right)
}

func TestFilesFromASendingMachineThatClosesRightAfterItsLastByteStillArrive(t *testing.T) {
	d := newFileDesk(t, "rogue", "right")
	d.rogueJoins("rogue", &rogueSide{sends: sendsAndClosesRightAfterItsLastByte("from the rogue")})
	right := d.sideJoins("right")
	d.waitUntilSharingFiles(right)
	d.waitUntilTheCentralSeesTakingPart("right", "rogue")

	d.focusOn(d.sides["rogue"].number)
	d.focusOn(right.number)

	arrived := waitUntilFilesWritten(t, "right", right.clipboard, 1)
	if content, err := os.ReadFile(arrived[0]); err != nil || string(content) != "from the rogue" {
		t.Errorf("right got %q, %v, want what the rogue sent", content, err)
	}
}

func TestAReceivingMachineThatNeverSaysEveryFileArrivedEndsTheRelayWithinTheDataSilenceLimit(t *testing.T) {
	d := newShortSilenceFileDesk(t, "left", "rogue")
	left := d.sideJoins("left")
	d.rogueJoins("rogue", &rogueSide{receives: receivesAndNeverSaysEveryFileArrived})
	d.waitUntilSharingFiles(left)
	d.waitUntilTheCentralSeesTakingPart("left", "rogue")
	d.focusOn(left.number)

	left.clipboard.personCopiesFiles(writeFile(t, filepath.Join(t.TempDir(), "small"), randomContent(t, 100<<10)))
	crossed := d.focusOn(d.sides["rogue"].number)
	d.log.waitFor(t, "The file copy from left to rogue stopped")
	took := time.Since(crossed)

	t.Logf("the relay to a receiver that never said every file arrived ended %s after focus moved, with a data silence limit of %s", took.Round(time.Millisecond), shortDataSilence)
	if took > 3*shortDataSilence {
		t.Errorf("the relay ended %s after focus moved, want within the data silence limit of %s after the last byte", took, shortDataSilence)
	}
	d.expectNoTransfersLeft(left)
}

func TestFocusFlippingDuringManyRelaysLeavesOnlyFilesThatReachedAClipboard(t *testing.T) {
	d := newFileDesk(t, "left", "right")
	left, right := d.sideJoins("left"), d.sideJoins("right")
	d.waitUntilSharingFiles(left, right)
	d.focusOn(left.number)
	source := t.TempDir()
	pauses := rand.New(rand.NewPCG(1, 2))

	for round := range 25 {
		left.clipboard.personCopiesFiles(writeFile(t, filepath.Join(source, fmt.Sprintf("round %d.bin", round)), randomContent(t, 512<<10)))
		d.focusOn(right.number)
		time.Sleep(time.Duration(pauses.IntN(80)) * time.Millisecond)
		d.focusOn(left.number)
	}

	d.expectNoTransfersLeft(left, right)
	var onTheClipboard []string
	for _, write := range right.clipboard.filesWritten() {
		for _, path := range write.paths {
			onTheClipboard = append(onTheClipboard, filepath.Base(filepath.Dir(path)))
		}
	}
	eventually(t, "right keeping only arrivals that reached its clipboard", func() bool {
		for _, name := range arrivalsLeftIn(t, right.arrivalDir) {
			if !slices.Contains(onTheClipboard, strings.TrimSuffix(name, ".complete")) {
				return false
			}
		}
		return true
	})
}

func TestAFileNameWithControlCharactersReachesTheLogEscaped(t *testing.T) {
	d := newFileDesk(t, "rogue")
	forged := strings.Repeat("x", 300) + "\nThe file copy from forged stopped\x1b[2J"
	d.rogueJoins("rogue", &rogueSide{sends: sendsUnderNames(forged)})
	d.waitUntilTheCentralSeesTakingPart("rogue")

	d.focusOn(d.sides["rogue"].number)
	d.focusOn(thisMachine)

	d.log.waitFor(t, `\nThe file copy from forged stopped\x1b[2J`)
	logged := d.log.String()
	if strings.Contains(logged, "\nThe file copy from forged") || strings.ContainsRune(logged, 0x1b) {
		t.Errorf("a name sent by another machine reached the log with its control characters:\n%q", logged)
	}
	expectNothingLeftIn(t, "the central", d.arrivalDir)
}

func TestASendingMachineThatTricklesIsCutOffAndItsRoomGivenBack(t *testing.T) {
	d := newShortSilenceFileDesk(t, "rogue")
	d.rogueJoins("rogue", &rogueSide{sends: trickles(1<<20, 200*time.Millisecond)})
	d.waitUntilTheCentralSeesTakingPart("rogue")

	d.focusOn(d.sides["rogue"].number)
	crossed := d.focusOn(thisMachine)
	d.log.waitFor(t, errTooSlow.Error())
	took := time.Since(crossed)

	t.Logf("a sender trickling a byte every 200 ms was cut off %s after focus moved, with a data silence limit of %s", took.Round(time.Millisecond), shortDataSilence)
	if took > 3*shortDataSilence {
		t.Errorf("the trickling sender was cut off %s after focus moved, want about the data silence limit of %s", took, shortDataSilence)
	}
	expectNothingLeftIn(t, "the central", d.arrivalDir)
	expectNoFilesWritten(t, "the central", d.clipboard)
	d.expectNoTransfersLeft()
}

func appendTo(t *testing.T, path, text string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer file.Close()
	if _, err := file.WriteString(text); err != nil {
		t.Fatalf("WriteString: %v", err)
	}
}

type fakeDataConn struct {
	chunk        int
	arrivesEvery time.Duration
	acksStuck    bool

	mutex         sync.Mutex
	readDeadline  time.Time
	writeDeadline time.Time
}

func (c *fakeDataConn) Read(buffer []byte) (int, error) {
	c.mutex.Lock()
	readBy, writeBy := c.readDeadline, c.writeDeadline
	c.mutex.Unlock()
	if c.acksStuck {
		if writeBy.IsZero() {
			time.Sleep(testPatience)
			return 0, errors.New("the acknowledgement never left")
		}
		time.Sleep(time.Until(writeBy))
		return 0, os.ErrDeadlineExceeded
	}
	if wait := time.Until(readBy); wait < c.arrivesEvery {
		time.Sleep(max(wait, 0))
		return 0, os.ErrDeadlineExceeded
	}
	time.Sleep(c.arrivesEvery)
	return min(c.chunk, len(buffer)), nil
}

func (c *fakeDataConn) Write(data []byte) (int, error)            { return len(data), nil }
func (c *fakeDataConn) Close() error                              { return nil }
func (c *fakeDataConn) TellAllArrived() error                     { return nil }
func (c *fakeDataConn) AwaitAllArrived(ctx context.Context) error { return nil }

func (c *fakeDataConn) SetDeadline(deadline time.Time) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.readDeadline, c.writeDeadline = deadline, deadline
	return nil
}

func (c *fakeDataConn) SetWriteDeadline(deadline time.Time) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.writeDeadline = deadline
	return nil
}

func TestReadingADataConnectionBoundsTheAcknowledgementItOwesWithTheSameDeadline(t *testing.T) {
	data := watch(&fakeDataConn{acksStuck: true}, 200*time.Millisecond)

	started := time.Now()
	_, err := data.Read(make([]byte, 1024))

	if took := time.Since(started); took > 2*time.Second || !errors.Is(err, errTooSlow) {
		t.Errorf("a read whose acknowledgement cannot leave returned %v after %s, want it cut off at the deadline", err, took)
	}
}

func TestAReaderGettingFewerThan256KiBWithinTheDataSilenceLimitOfWaitingIsCutOff(t *testing.T) {
	silence := 300 * time.Millisecond
	data := watch(&fakeDataConn{chunk: 1, arrivesEvery: 50 * time.Millisecond}, silence)

	started := time.Now()
	var read int
	var err error
	for err == nil {
		var n int
		n, err = data.Read(make([]byte, 1024))
		read += n
	}
	took := time.Since(started)

	if !errors.Is(err, errTooSlow) || took > 4*silence {
		t.Errorf("a trickle of a byte every 50 ms ended with %v after %s and %d bytes, want it cut off as too slow after about %s of waiting", err, took, read, silence)
	}
}

func TestTimeThisMachineSpendsBetweenReadsDoesNotCountAgainstTheOtherMachine(t *testing.T) {
	silence := 300 * time.Millisecond
	data := watch(&fakeDataConn{chunk: 1 << 10, arrivesEvery: time.Millisecond}, silence)

	for range 20 {
		if _, err := data.Read(make([]byte, 1<<10)); err != nil {
			t.Fatalf("a read after this machine was busy for a while failed: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
