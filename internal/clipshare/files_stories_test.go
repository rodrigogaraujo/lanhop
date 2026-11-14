package clipshare

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
	"github.com/rodrigogaraujo/lanhop/internal/filestream"
)

func copiedFolder(t *testing.T, top string) string {
	t.Helper()
	makeFolder(t, top)
	writeFile(t, filepath.Join(top, "beach.jpg"), randomContent(t, 200<<10))
	writeFile(t, filepath.Join(top, "trips", "alps.jpg"), randomContent(t, 70<<10))
	writeFile(t, filepath.Join(top, "trips", "empty.txt"), nil)
	makeFolder(t, filepath.Join(top, "to sort"))
	return top
}

func TestAFileCopyOnTheCentralArrivesWholeOnTheSideMachineThatGetsFocus(t *testing.T) {
	d := newFileDesk(t, "right")
	right := d.sideJoins("right")
	d.waitUntilSharingFiles(right)
	source := t.TempDir()
	report := writeFile(t, filepath.Join(source, "relatório de março.txt"), []byte("números do trimestre\n"))
	photos := copiedFolder(t, filepath.Join(source, "photos"))

	d.clipboard.personCopiesFiles(report, photos)
	d.focusOn(right.number)

	arrived := waitUntilFilesWritten(t, "right", right.clipboard, 1)
	expectSameFiles(t, arrived, right.arrivalDir, report, photos)
	if writes := right.clipboard.written(); len(writes) > 0 {
		t.Errorf("right was also written the text %s", describe(writes[0].text))
	}
	time.Sleep(settling)
	if writes := right.clipboard.filesWritten(); len(writes) != 1 {
		t.Errorf("right got files on its clipboard %d times, want once", len(writes))
	}
}

func TestAFileCopyOnASideMachineArrivesOnTheCentralOnlyWhenFocusComesBack(t *testing.T) {
	d := newFileDesk(t, "left")
	left := d.sideJoins("left")
	d.waitUntilSharingFiles(left)
	d.focusOn(left.number)
	notes := writeFile(t, filepath.Join(t.TempDir(), "notes.md"), randomContent(t, 300<<10))

	left.clipboard.personCopiesFiles(notes)
	time.Sleep(settling)
	expectNoFilesWritten(t, "the central, while focus stays on the left", d.clipboard)
	d.focusOn(thisMachine)

	arrived := waitUntilFilesWritten(t, "the central", d.clipboard, 1)
	expectSameFiles(t, arrived, d.arrivalDir, notes)
}

func TestAFileCopyGoesFromOneSideMachineToAnotherThroughTheCentralWhichKeepsNoCopyUntilFocusComesBack(t *testing.T) {
	d := newFileDesk(t, "left", "right")
	left, right := d.sideJoins("left"), d.sideJoins("right")
	d.waitUntilSharingFiles(left, right)
	d.focusOn(left.number)
	project := copiedFolder(t, filepath.Join(t.TempDir(), "project"))

	left.clipboard.personCopiesFiles(project)
	d.focusOn(right.number)

	arrived := waitUntilFilesWritten(t, "right", right.clipboard, 1)
	expectSameFiles(t, arrived, right.arrivalDir, project)
	time.Sleep(settling)
	expectNoFilesWritten(t, "the central, while focus is on the right", d.clipboard)
	if left := arrivalsLeftIn(t, d.arrivalDir); len(left) > 0 {
		t.Errorf("the central kept %q of a copy that only passed through it", left)
	}

	d.focusOn(thisMachine)
	arrivedHere := waitUntilFilesWritten(t, "the central", d.clipboard, 1)
	expectSameFiles(t, arrivedHere, d.arrivalDir, project)
	d.focusOn(left.number)
	time.Sleep(settling)
	expectNoFilesWritten(t, "left, where the files were copied", left.clipboard)
}

func TestAMachineThatTakesTextButNotFilesGetsNothingForAFileCopyAndSaysOnceWhy(t *testing.T) {
	d := newFileDesk(t, "textonly")
	textonly := d.sideJoinsWithoutFiles("textonly")
	d.waitUntilTakingPart(textonly)
	report := writeFile(t, filepath.Join(t.TempDir(), "report.pdf"), randomContent(t, 4<<10))

	d.clipboard.personCopiesFiles(report)
	d.focusOn(textonly.number)
	time.Sleep(settling)
	expectUnwritten(t, "textonly, after a file copy", textonly.clipboard, "what textonly held before")

	d.focusOn(thisMachine)
	d.clipboard.personCopies("text after the files")
	d.focusOn(textonly.number)
	waitUntilItHolds(t, "textonly", textonly.clipboard, "text after the files")
	for _, write := range textonly.clipboard.written() {
		if strings.Contains(write.text, "report.pdf") {
			t.Errorf("textonly got the name of a copied file as text: %s", describe(write.text))
		}
	}
	why := "File sharing is off on this machine, because " + errClipboardHoldsNoFiles.Error()
	textonly.log.waitFor(t, why)
	if said := strings.Count(textonly.log.String(), why); said != 1 {
		t.Errorf("textonly said %d times why it cannot share files, want once:\n%s", said, textonly.log.String())
	}
}

func TestFilesSwitchedOffOnOneEndMoveNoFileWhileTextKeepsMoving(t *testing.T) {
	d := newFileDesk(t, "left", "right")
	left, right := d.sides["left"], d.sides["right"]
	right.filesOn.Store(false)
	d.sideJoins("left")
	d.sideJoins("right")
	d.waitUntilSharingFiles(left)
	d.waitUntilTakingPart(right)
	source := t.TempDir()

	d.clipboard.personCopiesFiles(writeFile(t, filepath.Join(source, "from the central"), []byte("central")))
	d.focusOn(right.number)
	right.clipboard.personCopiesFiles(writeFile(t, filepath.Join(source, "from the right"), []byte("right")))
	d.focusOn(left.number)
	right.log.waitFor(t, clipboard.ErrFilesNotShared.Error())
	time.Sleep(settling)
	expectNoFilesWritten(t, "right, with files off", right.clipboard)
	expectNoFilesWritten(t, "left, after a file copy on the right that has files off", left.clipboard)

	d.focusOn(right.number)
	right.clipboard.personCopies("text from the right")
	d.focusOn(left.number)
	waitUntilItHolds(t, "left", left.clipboard, "text from the right")

	d.filesOn.Store(false)
	left.clipboard.personCopiesFiles(writeFile(t, filepath.Join(source, "from the left"), []byte("left")))
	d.focusOn(thisMachine)
	waitForAnyLog(t, clipboard.ErrFilesNotShared.Error(), left.log, d.log)
	d.focusOn(left.number)
	left.clipboard.personCopies("text from the left")
	d.focusOn(thisMachine)
	waitUntilItHolds(t, "the central, with files off", d.clipboard, "text from the left")
	expectNoFilesWritten(t, "the central, with files off", d.clipboard)
}

func TestFocusThatLeavesInTheMiddleOfAFileTransferLeavesNothingBehindAndTheNextVisitStartsItAgain(t *testing.T) {
	d := newSlowFileDesk(t, oneMebibytePerSecond, "right")
	right := d.sideJoins("right")
	d.waitUntilSharingFiles(right)
	large := writeFile(t, filepath.Join(t.TempDir(), "video.mp4"), randomContent(t, 3<<20))

	d.clipboard.personCopiesFiles(large)
	d.focusOn(right.number)
	waitUntilArriving(t, "right", right.arrivalDir)
	d.focusOn(thisMachine)

	expectNothingLeftIn(t, "right", right.arrivalDir)
	time.Sleep(settling)
	expectNoFilesWritten(t, "right, which focus left in the middle", right.clipboard)

	d.focusOn(right.number)
	arrived := waitUntilFilesWritten(t, "right, at the next visit", right.clipboard, 1)
	expectSameFiles(t, arrived, right.arrivalDir, large)
}

func TestAFileTransferCutByALostDataConnectionLeavesNothingAndIsTriedAgainAtTheNextVisit(t *testing.T) {
	d := newSlowFileDesk(t, oneMebibytePerSecond, "left")
	left := d.sides["left"]
	data := cuttableProxyTo(t, d.address)
	left.dataAddress = data.address
	d.sideJoins("left")
	d.waitUntilSharingFiles(left)
	large := writeFile(t, filepath.Join(t.TempDir(), "backup.tar"), randomContent(t, 3<<20))

	d.clipboard.personCopiesFiles(large)
	d.focusOn(left.number)
	waitUntilArriving(t, "left", left.arrivalDir)
	data.cut()

	expectNothingLeftIn(t, "left", left.arrivalDir)
	d.log.waitFor(t, "The file copy to left stopped")
	time.Sleep(settling)
	expectNoFilesWritten(t, "left, after the data connection was cut", left.clipboard)

	d.focusOn(thisMachine)
	d.focusOn(left.number)
	arrived := waitUntilFilesWritten(t, "left, at the next visit", left.clipboard, 1)
	expectSameFiles(t, arrived, left.arrivalDir, large)
}

func TestALostControlConnectionLeavesNothingOfAFileTransferBehind(t *testing.T) {
	d := newSlowFileDesk(t, oneMebibytePerSecond, "left")
	left := d.sides["left"]
	control := cuttableProxyTo(t, d.address)
	d.startSide(left, control.address, d.clipboardOpener(left.clipboard))
	d.waitUntilSharingFiles(left)
	large := writeFile(t, filepath.Join(t.TempDir(), "disk.img"), randomContent(t, 3<<20))

	d.clipboard.personCopiesFiles(large)
	d.focusOn(left.number)
	waitUntilArriving(t, "left", left.arrivalDir)
	control.cut()

	expectNothingLeftIn(t, "left", left.arrivalDir)
	time.Sleep(settling)
	expectNoFilesWritten(t, "left, after its control connection was lost", left.clipboard)
}

func TestAFileThatChangesWhileItIsSentArrivesNowhere(t *testing.T) {
	d := newSlowFileDesk(t, oneMebibytePerSecond, "right")
	right := d.sideJoins("right")
	d.waitUntilSharingFiles(right)
	log := writeFile(t, filepath.Join(t.TempDir(), "server.log"), randomContent(t, 3<<20))

	d.clipboard.personCopiesFiles(log)
	d.focusOn(right.number)
	waitUntilArriving(t, "right", right.arrivalDir)
	growing, err := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	if _, err := growing.WriteString("one more line\n"); err != nil {
		t.Fatalf("WriteString: %v", err)
	}
	growing.Close()

	d.log.waitFor(t, filestream.ErrChanged.Error())
	expectNothingLeftIn(t, "right", right.arrivalDir)
	time.Sleep(settling)
	expectNoFilesWritten(t, "right", right.clipboard)
}

func TestAFileCopyThatDoesNotFitInTheRoomIsNotTakenAndTheLogSaysWhy(t *testing.T) {
	d := newFileDesk(t, "right")
	right := d.sides["right"]
	right.arrivalDir, right.arrivals = openArrivals(t, 1<<20)
	d.sideJoins("right")
	d.waitUntilSharingFiles(right)

	d.clipboard.personCopiesFiles(writeFile(t, filepath.Join(t.TempDir(), "too large"), randomContent(t, 2<<20)))
	d.focusOn(right.number)

	right.log.waitFor(t, "do not fit in the room lanhop allows itself")
	d.log.waitFor(t, "The file copy to right stopped")
	expectNothingLeftIn(t, "right", right.arrivalDir)
	expectNoFilesWritten(t, "right", right.clipboard)
}

func TestNamesThatWouldLeaveTheArrivalFolderAreRefusedEndToEnd(t *testing.T) {
	d := newFileDesk(t, "rogue")
	around := filepath.Dir(d.arrivalDir)
	names := []string{"../escaped.txt", filepath.Join(around, "absolute.txt"), "inside/../../escaped.txt"}
	d.rogueJoins("rogue", &rogueSide{sends: sendsUnderNames(names...)})
	d.waitUntilTheCentralSeesTakingPart("rogue")

	for _, name := range names {
		d.focusOn(d.sides["rogue"].number)
		d.focusOn(thisMachine)
		d.log.waitFor(t, strconv.Quote(name))
	}

	expectNothingLeftIn(t, "the central", d.arrivalDir)
	expectNoFilesWritten(t, "the central", d.clipboard)
	listed, err := os.ReadDir(around)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range listed {
		if entry.Name() != filepath.Base(d.arrivalDir) {
			t.Errorf("%q appeared next to the arrival folder", entry.Name())
		}
	}
}

func TestAFileCopyOverTheLimitOrHoldingAFileThatCannotBeSentIsNotSentAndTheLogSaysWhy(t *testing.T) {
	d := newFileDesk(t, "right")
	right := d.sideJoins("right")
	d.waitUntilSharingFiles(right)
	source := t.TempDir()
	huge, err := os.Create(filepath.Join(source, "huge.bin"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := huge.Truncate(clipboard.FileCopyLimit + 1); err != nil {
		t.Fatalf("Truncate: %v", err)
	}
	huge.Close()
	pipe := filepath.Join(source, "pipe")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Fatalf("Mkfifo: %v", err)
	}

	d.clipboard.personCopiesFiles(huge.Name())
	d.focusOn(right.number)
	d.log.waitFor(t, clipboard.ErrFilesTooLarge.Error())
	d.focusOn(thisMachine)
	d.clipboard.personCopiesFiles(pipe)
	d.focusOn(right.number)
	d.log.waitFor(t, filestream.ErrNotSendable.Error())
	time.Sleep(settling)
	expectNoFilesWritten(t, "right", right.clipboard)

	d.focusOn(thisMachine)
	project := makeFolder(t, filepath.Join(source, "project"))
	writeFile(t, filepath.Join(project, "main.go"), []byte("package main\n"))
	if err := os.Symlink(source, filepath.Join(project, "outside")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	d.clipboard.personCopiesFiles(project)
	d.focusOn(right.number)
	arrived := waitUntilFilesWritten(t, "right", right.clipboard, 1)
	d.log.waitFor(t, "leaves out 1 link or special file")
	if _, err := os.Lstat(filepath.Join(arrived[0], "outside")); err == nil {
		t.Error("a link inside the copied folder arrived")
	}
	if content, err := os.ReadFile(filepath.Join(arrived[0], "main.go")); err != nil || string(content) != "package main\n" {
		t.Errorf("main.go arrived as %q, %v", content, err)
	}
}

func TestAFileCopyMadeBeforeFilesWereSwitchedOnWaitsUntilItIsCopiedAgain(t *testing.T) {
	d := newFileDesk(t, "left")
	left := d.sides["left"]
	left.filesOn.Store(false)
	d.sideJoins("left")
	d.waitUntilTakingPart(left)
	source := t.TempDir()
	d.focusOn(left.number)

	left.clipboard.personCopiesFiles(writeFile(t, filepath.Join(source, "early.txt"), []byte("before the switch")))
	left.filesOn.Store(true)
	d.focusOn(thisMachine)
	d.waitUntilSharingFiles(left)
	time.Sleep(settling)
	expectNoFilesWritten(t, "the central, after a copy made before files were on", d.clipboard)

	d.focusOn(left.number)
	again := writeFile(t, filepath.Join(source, "again.txt"), []byte("copied again"))
	left.clipboard.personCopiesFiles(again)
	d.focusOn(thisMachine)
	arrived := waitUntilFilesWritten(t, "the central", d.clipboard, 1)
	expectSameFiles(t, arrived, d.arrivalDir, again)
}

func TestFilesThatArriveAfterThePersonCopiedSomethingElseAreNotPutOnTheClipboard(t *testing.T) {
	d := newSlowFileDesk(t, oneMebibytePerSecond, "right")
	right := d.sideJoins("right")
	d.waitUntilSharingFiles(right)

	d.clipboard.personCopiesFiles(writeFile(t, filepath.Join(t.TempDir(), "slides.key"), randomContent(t, 2<<20)))
	d.focusOn(right.number)
	waitUntilArriving(t, "right", right.arrivalDir)
	right.clipboard.personCopies("copied on the right while the files travelled")

	right.log.waitFor(t, "Files arrived, and the newer copy made on this machine after focus arrived was kept")
	expectNoFilesWritten(t, "right", right.clipboard)
	expectNothingLeftIn(t, "right", right.arrivalDir)
	d.focusOn(thisMachine)
	waitUntilItHolds(t, "the central", d.clipboard, "copied on the right while the files travelled")
}

func TestASideMachineWhoseFilesAreSwitchedOffWhileFocusIsElsewhereSendsNothingMore(t *testing.T) {
	d := newFileDesk(t, "left", "right")
	left, right := d.sideJoins("left"), d.sideJoins("right")
	d.waitUntilSharingFiles(left, right)
	d.focusOn(left.number)
	secret := writeFile(t, filepath.Join(t.TempDir(), "secret.txt"), []byte("only for the right"))
	left.clipboard.personCopiesFiles(secret)
	d.focusOn(right.number)
	waitUntilFilesWritten(t, "right", right.clipboard, 1)

	left.filesOn.Store(false)
	d.focusOn(thisMachine)

	why := "asked for the file copy of this machine, and it is not sent, because file sharing is off on this machine"
	eventually(t, "the left saying why or the central getting the files", func() bool {
		return strings.Contains(left.log.String(), why) || len(d.clipboard.filesWritten()) > 0
	})
	time.Sleep(settling)
	expectNoFilesWritten(t, "the central, after files were switched off on the left", d.clipboard)
	left.log.waitFor(t, why)
	expectNothingLeftIn(t, "the central", d.arrivalDir)
}
