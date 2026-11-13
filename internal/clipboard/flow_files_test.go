package clipboard

import (
	"errors"
	"testing"

	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/features"
)

var (
	report  = FileCopy{FilesAndFolders: 1, Bytes: 10 << 20}
	project = FileCopy{FilesAndFolders: 120, Bytes: 300 << 20}
	atLimit = FileCopy{FilesAndFolders: FilesAndFoldersLimit, Bytes: FileCopyLimit}
)

func fetch(from string, transfer int, files FileCopy) action {
	return action{Do: Fetch, To: from, Transfer: transfer, FileCopy: files}
}

func deliver(to string, transfer int, files FileCopy) action {
	return action{Do: Deliver, To: to, Transfer: transfer, FileCopy: files}
}

func writeFiles(transfer int, files FileCopy) action {
	return action{Do: WriteFiles, Transfer: transfer, FileCopy: files}
}

func stop(transfer int) action { return action{Do: Stop, Transfer: transfer} }

func describe(files FileCopy) toCentral { return toCentral{Do: Describe, FileCopy: files} }
func sendFiles(transfer int) toCentral  { return toCentral{Do: SendFiles, Transfer: transfer} }
func receive(transfer int) toCentral    { return toCentral{Do: ReceiveFiles, Transfer: transfer} }
func written(transfer int) toCentral    { return toCentral{Do: WriteFiles, Transfer: transfer} }
func stopped(transfer int) toCentral    { return toCentral{Do: Stop, Transfer: transfer} }

func deskSharing(t *testing.T, kinds Kinds, sides ...string) *Desk[string] {
	t.Helper()
	d := NewDesk[string](kinds)
	for i, side := range sides {
		expect(t, "connect "+side, d.Connected(side), announce(side, kinds))
		var startsToTakePart []action
		if i == 0 {
			startsToTakePart = []action{{Do: Mark}}
		}
		expect(t, side+" says what it takes", d.Says(side, kinds), startsToTakePart...)
	}
	return d
}

func deskSharingFiles(t *testing.T, sides ...string) *Desk[string] {
	t.Helper()
	return deskSharing(t, Text|Files, sides...)
}

func readGaveFiles(t *testing.T, d *Desk[string], files FileCopy) []action {
	t.Helper()
	actions, err := d.ReadGaveFiles(files, nil)
	if err != nil {
		t.Fatalf("ReadGaveFiles(%+v): %v", files, err)
	}
	return actions
}

func fileCopyFrom(t *testing.T, d *Desk[string], side string, files FileCopy) []action {
	t.Helper()
	actions, err := d.FileCopyFrom(side, files)
	if err != nil {
		t.Fatalf("FileCopyFrom(%q, %+v): %v", side, files, err)
	}
	return actions
}

func aFileCopyOfAReaches(t *testing.T, d *Desk[string], side string) {
	t.Helper()
	d.FocusOn("A")
	d.ReadUnchanged()
	expect(t, "focus from A to "+side, d.FocusOn(side))
	expect(t, "the file copy of A arrives", fileCopyFrom(t, d, "A", report), fetch("A", 1, report), deliver(side, 1, report))
}

func sideTaking(t *testing.T, own, central Kinds) *Side {
	t.Helper()
	s := NewSide(own)
	expect(t, "connect", s.Connected(), toCentral{Do: Announce, Takes: own})
	expect(t, "the central says what it takes", s.CentralSays(central), toCentral{Do: Mark})
	s.FocusArrives()
	return s
}

func sideSharingFiles(t *testing.T) *Side {
	t.Helper()
	return sideTaking(t, Text|Files, Text|Files)
}

func describedFiles(t *testing.T, s *Side, files FileCopy) {
	t.Helper()
	expect(t, "focus leaves", s.FocusLeaves(), toCentral{Do: Read})
	described, err := s.ReadGaveFiles(files, nil)
	if err != nil {
		t.Fatalf("ReadGaveFiles(%+v): %v", files, err)
	}
	expect(t, "the side machine describes its file copy", described, describe(files))
}

func TestAFileCopyOnTheCentralGoesToTheSideMachineThatGetsFocus(t *testing.T) {
	d := deskSharingFiles(t, "A")

	expect(t, "focus to A", d.FocusOn("A"), read())
	expect(t, "the central read a file copy", readGaveFiles(t, d, report), deliver("A", 1, report))
	expect(t, "the files arrive on A", d.FilesArrived(1))
	expect(t, "focus back on the central", d.FocusOnCentral())
	expect(t, "A had nothing new", d.UnchangedOn("A"))
}

func TestAFileCopyOnASideMachineIsWrittenOnTheCentralOnlyOnceItsFilesHaveArrived(t *testing.T) {
	s := sideSharingFiles(t)
	describedFiles(t, s, report)
	expect(t, "the central asks for the files", s.CentralWantsFiles(41), sendFiles(41))

	d := deskSharingFiles(t, "A")
	d.FocusOn("A")
	d.ReadUnchanged()
	expect(t, "focus back on the central", d.FocusOnCentral())
	expect(t, "the file copy of A arrives", fileCopyFrom(t, d, "A", report), fetch("A", 1, report))
	expect(t, "the files of A arrive", d.FilesArrived(1), writeFiles(1, report))
	expect(t, "the same transfer reports its files again", d.FilesArrived(1))
}

func TestAFileCopyGoesFromOneSideMachineToAnotherThroughTheCentral(t *testing.T) {
	d := deskSharingFiles(t, "A", "B")

	aFileCopyOfAReaches(t, d, "B")
	expect(t, "the files reach B", d.FilesArrived(1))
	expect(t, "focus back to A", d.FocusOn("A"))
	expect(t, "B had nothing new", d.UnchangedOn("B"))
}

func TestAFileCopyPassingThroughTheCentralIsWrittenThereOnlyWhenFocusComesBack(t *testing.T) {
	d := deskSharingFiles(t, "A", "B")
	aFileCopyOfAReaches(t, d, "B")
	d.FilesArrived(1)

	expect(t, "focus back on the central", d.FocusOnCentral())
	expect(t, "B had nothing new", d.UnchangedOn("B"), fetch("A", 2, report))
	expect(t, "the files reach the central", d.FilesArrived(2), writeFiles(2, report))
}

func TestAFileCopyThatVisitedTheCentralIsFetchedAgainFromWhereItWasCopied(t *testing.T) {
	d := deskSharingFiles(t, "A", "B")
	d.FocusOn("A")
	d.ReadUnchanged()
	d.FocusOnCentral()
	expect(t, "the file copy of A arrives at the central", fileCopyFrom(t, d, "A", report), fetch("A", 1, report))
	expect(t, "the files of A arrive", d.FilesArrived(1), writeFiles(1, report))

	expect(t, "focus to B", d.FocusOn("B"), read())
	expect(t, "the central still holds the files of A", d.ReadUnchanged(), fetch("A", 2, report), deliver("B", 2, report))
}

func TestAFileCopyReachesOnlyTheMachinesThatGetFocus(t *testing.T) {
	d := deskSharingFiles(t, "A", "B")
	expect(t, "focus to A", d.FocusOn("A"), read())
	expect(t, "the central read a file copy", readGaveFiles(t, d, report), deliver("A", 1, report))
	d.FilesArrived(1)

	expect(t, "focus back on the central", d.FocusOnCentral())
	expect(t, "A had nothing new", d.UnchangedOn("A"))
	expect(t, "focus to A again", d.FocusOn("A"), read())
	expect(t, "the central had nothing new", d.ReadUnchanged())
	expect(t, "focus to B, later", d.FocusOn("B"))
	expect(t, "A had nothing new", d.UnchangedOn("A"), deliver("B", 2, report))
}

func TestTheNewestCopyWinsBetweenTextAndFiles(t *testing.T) {
	tests := []struct {
		name      string
		onA       func(d *Desk[string]) []action
		onAWrites []action
		central   func(t *testing.T, d *Desk[string]) []action
		toB       []action
	}{
		{
			name:      "text on the central after files on A",
			onA:       func(d *Desk[string]) []action { actions, _ := d.FileCopyFrom("A", report); return actions },
			onAWrites: []action{fetch("A", 1, report)},
			central:   func(t *testing.T, d *Desk[string]) []action { return readGave(t, d, "newer text") },
			toB:       []action{send("B", "newer text")},
		},
		{
			name:      "files on the central after text on A",
			onA:       func(d *Desk[string]) []action { return d.CopyFrom("A", "older text") },
			onAWrites: []action{write("older text")},
			central:   func(t *testing.T, d *Desk[string]) []action { return readGaveFiles(t, d, project) },
			toB:       []action{deliver("B", 1, project)},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			d := deskSharingFiles(t, "A", "B")
			d.FocusOn("A")
			d.ReadUnchanged()
			d.FocusOnCentral()
			expect(t, "the copy of A arrives at the central", test.onA(d), test.onAWrites...)
			d.FilesArrived(1)

			expect(t, "focus to B", d.FocusOn("B"), read())
			expect(t, "the central made a newer copy", test.central(t, d), test.toB...)
		})
	}
}

func TestAFileTransferStopsWhenANewerCopyReachesTheDesk(t *testing.T) {
	d := deskSharingFiles(t, "A", "B")
	d.FocusOn("A")
	d.ReadUnchanged()
	d.FocusOnCentral()
	d.GaveUpOn("A")
	expect(t, "focus to B", d.FocusOn("B"), read())
	expect(t, "the central read a file copy", readGaveFiles(t, d, project), deliver("B", 1, project))

	expect(t, "the late text of A arrives", d.CopyFrom("A", "late text"), stop(1), send("B", "late text"))
	expect(t, "the files of the stopped transfer arrive anyway", d.FilesArrived(1))
}

func TestAFileTransferStopsWhenFocusLeavesTheMachineReceivingItAndStartsAgainAtTheNextVisit(t *testing.T) {
	d := deskSharingFiles(t, "A", "B")
	expect(t, "focus to A", d.FocusOn("A"), read())
	expect(t, "the central read a file copy", readGaveFiles(t, d, project), deliver("A", 1, project))

	expect(t, "focus passes on to B before the files have arrived", d.FocusOn("B"), stop(1))
	expect(t, "the files of the stopped transfer arrive anyway", d.FilesArrived(1))
	expect(t, "A had nothing new", d.UnchangedOn("A"), deliver("B", 2, project))
	expect(t, "the files reach B", d.FilesArrived(2))
	expect(t, "focus back to A", d.FocusOn("A"))
	expect(t, "B had nothing new", d.UnchangedOn("B"), deliver("A", 3, project))
}

func TestAFileTransferToTheCentralStopsWhenFocusLeavesIt(t *testing.T) {
	d := deskSharingFiles(t, "A", "B")
	d.FocusOn("A")
	d.ReadUnchanged()
	d.FocusOnCentral()
	expect(t, "the file copy of A arrives", fileCopyFrom(t, d, "A", project), fetch("A", 1, project))

	expect(t, "focus to B before the files have arrived", d.FocusOn("B"), read(), stop(1))
	expect(t, "the central had nothing new", d.ReadUnchanged(), fetch("A", 2, project), deliver("B", 2, project))
	expect(t, "the files of the stopped transfer arrive anyway", d.FilesArrived(1))
	expect(t, "focus back on the central before B has them", d.FocusOnCentral(), stop(2))
	expect(t, "B had nothing new", d.UnchangedOn("B"), fetch("A", 3, project))
}

func TestAFileTransferStopsWhenTheMachineReceivingItCanNoLongerTakeIt(t *testing.T) {
	ends := []struct {
		name string
		end  func(d *Desk[string]) []action
		want []action
	}{
		{"the receiving machine is lost", func(d *Desk[string]) []action { return d.Lost("A") }, []action{stop(1)}},
		{"the receiving machine stops taking files", func(d *Desk[string]) []action { return d.Says("A", Text) }, []action{stop(1)}},
		{"the central switches files off", func(d *Desk[string]) []action { return d.Switch(Text) }, []action{announce("A", Text), announce("B", Text), stop(1)}},
	}
	for _, test := range ends {
		t.Run(test.name, func(t *testing.T) {
			d := deskSharingFiles(t, "A", "B")
			d.FocusOn("A")
			expect(t, "the central read a file copy", readGaveFiles(t, d, project), deliver("A", 1, project))

			expect(t, "the transfer can no longer go on", test.end(d), test.want...)
		})
	}
}

func TestAFileTransferThatEndsEarlyIsTriedAgainOnlyAtTheNextVisit(t *testing.T) {
	d := deskSharingFiles(t, "A", "B")
	d.FocusOn("A")
	expect(t, "the central read a file copy", readGaveFiles(t, d, report), deliver("A", 1, report))
	d.TransferEnded(7)
	d.TransferEnded(1)

	expect(t, "C connects while A has focus", d.Connected("C"), announce("C", Text|Files))
	expect(t, "C takes text and files", d.Says("C", Text|Files))
	expect(t, "focus back on the central", d.FocusOnCentral())
	expect(t, "A had nothing new", d.UnchangedOn("A"))
	expect(t, "focus to A again", d.FocusOn("A"), read())
	expect(t, "the central had nothing new", d.ReadUnchanged(), deliver("A", 2, report))
}

func TestAFileCopyWaitsForTheAnswerOfTheMachineThatLostFocus(t *testing.T) {
	answers := []struct {
		name   string
		answer func(d *Desk[string]) []action
		toB    []action
	}{
		{"A had nothing new", func(d *Desk[string]) []action { return d.UnchangedOn("A") }, []action{deliver("B", 2, report)}},
		{"A emptied its clipboard", func(d *Desk[string]) []action { return d.WithdrawalFrom("A") }, nil},
		{"A did not answer in time", func(d *Desk[string]) []action { return d.GaveUpOn("A") }, nil},
	}
	for _, test := range answers {
		t.Run(test.name, func(t *testing.T) {
			d := deskSharingFiles(t, "A", "B")
			d.FocusOn("A")
			expect(t, "the central read a file copy", readGaveFiles(t, d, report), deliver("A", 1, report))
			d.FilesArrived(1)

			expect(t, "focus from A to B", d.FocusOn("B"))
			expect(t, "the answer of A", test.answer(d), test.toB...)
		})
	}
}

func TestAMachineNeverGetsBackItsOwnFileCopy(t *testing.T) {
	d := deskSharingFiles(t, "A", "B")
	aFileCopyOfAReaches(t, d, "B")

	expect(t, "focus back to A before B has the files", d.FocusOn("A"), stop(1))
	expect(t, "B had nothing new", d.UnchangedOn("B"))
	expect(t, "A stops taking text and keeps taking files", d.Says("A", Files))
}

func TestAFileCopyReadAgainIsANewCopyEvenWithTheSameDescription(t *testing.T) {
	d := deskSharingFiles(t, "A")
	d.FocusOn("A")
	expect(t, "the central read a file copy", readGaveFiles(t, d, report), deliver("A", 1, report))
	d.FilesArrived(1)
	d.FocusOnCentral()
	d.UnchangedOn("A")

	expect(t, "focus to A again", d.FocusOn("A"), read())
	expect(t, "the central copied files of the same number and size", readGaveFiles(t, d, report), deliver("A", 2, report))
}

func TestAFileCopyIsForgottenWhenTheMachineWhereItWasCopiedCanNoLongerSendIt(t *testing.T) {
	ends := []struct {
		name string
		end  func(d *Desk[string]) []action
		want []action
	}{
		{"its connection is lost", func(d *Desk[string]) []action { return d.Lost("A") }, nil},
		{"it connects again", func(d *Desk[string]) []action { return d.Connected("A") }, []action{announce("A", Text|Files)}},
		{"it stops taking files", func(d *Desk[string]) []action { return d.Says("A", Text) }, nil},
	}
	for _, test := range ends {
		t.Run(test.name, func(t *testing.T) {
			d := deskSharingFiles(t, "A", "B")
			aFileCopyOfAReaches(t, d, "B")
			d.FilesArrived(1)

			expect(t, "A can no longer send its files", test.end(d), test.want...)
			expect(t, "focus to the central", d.FocusOnCentral())
			expect(t, "B had nothing new", d.UnchangedOn("B"))
		})
	}
}

func TestASideMachineThatTakesTextButNotFilesGetsNothingForAFileCopy(t *testing.T) {
	d := deskSharingFiles(t, "A")
	d.Connected("B")
	expect(t, "B takes text only", d.Says("B", Text))
	expect(t, "focus to B", d.FocusOn("B"), read())
	expect(t, "the central read a text", readGave(t, d, "earlier text"), send("B", "earlier text"))
	d.FocusOnCentral()
	d.UnchangedOn("B")

	expect(t, "focus to B again", d.FocusOn("B"), read())
	expect(t, "the central read a file copy", readGaveFiles(t, d, report))
	expect(t, "focus to A", d.FocusOn("A"))
	expect(t, "B had nothing new", d.UnchangedOn("B"), deliver("A", 1, report))
}

func TestASideMachineThatTakesTextButNotFilesWithdrawsItsFileCopy(t *testing.T) {
	sides := []struct {
		name    string
		own     Kinds
		central Kinds
	}{
		{"its own switch for files is off", Text, Text | Files},
		{"the central does not take files", Text | Files, Text},
	}
	for _, test := range sides {
		t.Run(test.name, func(t *testing.T) {
			s := sideTaking(t, test.own, test.central)

			expect(t, "the central asks for files", s.CentralWantsFiles(1), stopped(1))
			expect(t, "the central sends files", s.CentralSendsFiles(2), stopped(2))
			expect(t, "focus leaves", s.FocusLeaves(), toCentral{Do: Read})
			sent, err := s.ReadGaveFiles(report, nil)
			if !errors.Is(err, ErrFilesNotShared) {
				t.Errorf("error = %v, want %v", err, ErrFilesNotShared)
			}
			expect(t, "a file copy", sent, toCentral{Do: Withdraw})
			expect(t, "the central still asks for files", s.CentralWantsFiles(3), stopped(3))
			sent, err = s.ReadGave("a text", nil)
			if err != nil {
				t.Fatalf("ReadGave: %v", err)
			}
			expect(t, "a text", sent, toCentral{Do: Send, Text: "a text"})
		})
	}
}

func TestAFileCopyFromASideMachineThatDoesNotShareFilesCountsAsAWithdrawal(t *testing.T) {
	d := deskSharingFiles(t, "A", "B")
	d.Says("A", Text)
	copyOnTheCentralGoesTo(t, d, "A", "earlier text")
	expect(t, "focus from A to B", d.FocusOn("B"))

	actions, err := d.FileCopyFrom("A", report)

	if !errors.Is(err, ErrFilesNotShared) {
		t.Errorf("error = %v, want %v", err, ErrFilesNotShared)
	}
	expect(t, "B keeps what it held", actions)
}

func TestASideMachineThatTakesFilesButNotTextGetsFilesAndNoText(t *testing.T) {
	d := deskSharingFiles(t, "A")
	d.Connected("B")
	expect(t, "B takes files only", d.Says("B", Files))
	expect(t, "focus to B", d.FocusOn("B"), read())
	expect(t, "the central read a text", readGave(t, d, "a text"))
	d.FocusOnCentral()
	d.UnchangedOn("B")

	expect(t, "focus to B again", d.FocusOn("B"), read())
	expect(t, "the central read a file copy", readGaveFiles(t, d, report), deliver("B", 1, report))
	expect(t, "the files reach B", d.FilesArrived(1))
	expect(t, "focus to A", d.FocusOn("A"))
	expect(t, "B answers with a text it does not share", d.CopyFrom("B", "a text B should have kept"))
}

func TestASideMachineThatTakesFilesButNotTextWithdrawsItsText(t *testing.T) {
	s := sideTaking(t, Files, Text|Files)

	expect(t, "a text arrives", s.CopyFromCentral("x"))
	expect(t, "focus leaves", s.FocusLeaves(), toCentral{Do: Read})
	sent, err := s.ReadGave("a text", nil)
	if !errors.Is(err, ErrTextNotShared) {
		t.Errorf("error = %v, want %v", err, ErrTextNotShared)
	}
	expect(t, "a text", sent, toCentral{Do: Withdraw})
	s.FocusArrives()
	describedFiles(t, s, report)
}

func TestAFileCopyThatIsNotCarriedWithdrawsTheLastOne(t *testing.T) {
	tooSlow := errors.New("listing the files took longer than an answer may take")
	tests := []struct {
		name    string
		on      Kinds
		files   FileCopy
		readErr error
		want    error
	}{
		{"a file copy while files are off on the central", Text, report, nil, ErrFilesNotShared},
		{"more than 10 000 files and folders", Text | Files, FileCopy{FilesAndFolders: FilesAndFoldersLimit + 1, Bytes: 1}, nil, ErrTooManyFiles},
		{"more than 4 GiB", Text | Files, FileCopy{FilesAndFolders: 1, Bytes: FileCopyLimit + 1}, nil, ErrFilesTooLarge},
		{"no file at all", Text | Files, FileCopy{}, nil, ErrEmpty},
		{"a list too slow to count", Text | Files, FileCopy{}, tooSlow, tooSlow},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			d := deskSharing(t, test.on, "A", "B")
			copyOnTheCentralGoesTo(t, d, "A", "earlier text")
			d.FocusOnCentral()
			d.UnchangedOn("A")

			expect(t, "focus to B", d.FocusOn("B"), read())
			actions, err := d.ReadGaveFiles(test.files, test.readErr)

			if !errors.Is(err, test.want) {
				t.Errorf("error = %v, want %v", err, test.want)
			}
			expect(t, "B keeps what it held", actions)
		})
	}
}

func TestAFileCopyOverTheLimitFromASideMachineCountsAsAWithdrawal(t *testing.T) {
	tests := []struct {
		name  string
		files FileCopy
		want  error
	}{
		{"more than 10 000 files and folders", FileCopy{FilesAndFolders: FilesAndFoldersLimit + 1, Bytes: 1}, ErrTooManyFiles},
		{"more than 4 GiB", FileCopy{FilesAndFolders: 1, Bytes: FileCopyLimit + 1}, ErrFilesTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			d := deskSharingFiles(t, "A", "B")
			copyOnTheCentralGoesTo(t, d, "A", "earlier text")
			expect(t, "focus from A to B", d.FocusOn("B"))

			actions, err := d.FileCopyFrom("A", test.files)

			if !errors.Is(err, test.want) {
				t.Errorf("error = %v, want %v", err, test.want)
			}
			expect(t, "B keeps what it held", actions)
		})
	}
}

func TestASideMachineWithdrawsAFileCopyItCannotSend(t *testing.T) {
	tooSlow := errors.New("listing the files took longer than an answer may take")
	tests := []struct {
		name    string
		files   FileCopy
		readErr error
		want    error
	}{
		{"more than 10 000 files and folders", FileCopy{FilesAndFolders: FilesAndFoldersLimit + 1, Bytes: 1}, nil, ErrTooManyFiles},
		{"more than 4 GiB", FileCopy{FilesAndFolders: 1, Bytes: FileCopyLimit + 1}, nil, ErrFilesTooLarge},
		{"no file at all", FileCopy{}, nil, ErrEmpty},
		{"a list too slow to count", FileCopy{}, tooSlow, tooSlow},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := sideSharingFiles(t)
			s.FocusLeaves()

			sent, err := s.ReadGaveFiles(test.files, test.readErr)

			if !errors.Is(err, test.want) {
				t.Errorf("error = %v, want %v", err, test.want)
			}
			expect(t, "the file copy is withdrawn", sent, toCentral{Do: Withdraw})
			expect(t, "the central asks for the files anyway", s.CentralWantsFiles(1), stopped(1))
		})
	}
}

func TestAFileCopyAtTheLimitIsSent(t *testing.T) {
	d := deskSharingFiles(t, "A")
	expect(t, "focus to A", d.FocusOn("A"), read())
	expect(t, "the central read a file copy at the limit", readGaveFiles(t, d, atLimit), deliver("A", 1, atLimit))

	s := sideSharingFiles(t)
	describedFiles(t, s, atLimit)
}

func TestSideMachinesWithoutFilesGetNoFileAction(t *testing.T) {
	d := deskSharingFiles(t, "B")
	d.Connected("old")
	d.Connected("text only")
	expect(t, "a side machine takes text only", d.Says("text only", Text))

	expect(t, "focus to the old side machine", d.FocusOn("old"), read())
	expect(t, "the central read a file copy", readGaveFiles(t, d, report))
	expect(t, "focus to the side machine that takes text only", d.FocusOn("text only"))
	expect(t, "frames from the old side machine", fileCopyFrom(t, d, "old", project))
	expect(t, "focus to B", d.FocusOn("B"))
	expect(t, "the side machine that takes text only had nothing new", d.UnchangedOn("text only"), deliver("B", 1, report))
}

func TestASideMachineUnderACentralWithoutFilesNeverMovesFiles(t *testing.T) {
	s := NewSide(Text | Files)
	expect(t, "connect", s.Connected(), toCentral{Do: Announce, Takes: Text | Files})
	s.FocusArrives()

	expect(t, "focus leaves before the central says anything", s.FocusLeaves())
	sent, err := s.ReadGaveFiles(report, nil)
	if err != nil {
		t.Fatalf("ReadGaveFiles: %v", err)
	}
	expect(t, "a file copy", sent)
	s.FocusArrives()
	expect(t, "the central asks for files", s.CentralWantsFiles(1), stopped(1))
	expect(t, "the central sends files", s.CentralSendsFiles(2), stopped(2))
	expect(t, "the central takes text only", s.CentralSays(Text), toCentral{Do: Mark})
	expect(t, "the central sends files again", s.CentralSendsFiles(3), stopped(3))
}

func TestASideMachineWritesFilesOnlyOnceTheyHaveArrived(t *testing.T) {
	s := sideSharingFiles(t)

	expect(t, "the central sends files", s.CentralSendsFiles(7), receive(7))
	expect(t, "the files of another transfer arrive", s.FilesArrived(8))
	expect(t, "the files arrive", s.FilesArrived(7), written(7))
	expect(t, "the same transfer reports its files again", s.FilesArrived(7))
	expect(t, "the central sends files again", s.CentralSendsFiles(9), receive(9))
	s.TransferEnded(9)
	expect(t, "the files of a transfer that ended arrive anyway", s.FilesArrived(9))
}

func TestASideMachineStopsReceivingFilesWhenFocusLeaves(t *testing.T) {
	s := sideSharingFiles(t)
	expect(t, "the central sends files", s.CentralSendsFiles(7), receive(7))

	expect(t, "focus leaves before they have arrived", s.FocusLeaves(), stopped(7), toCentral{Do: Read})
	expect(t, "the files arrive after focus left", s.FilesArrived(7))
	expect(t, "the central sends files while focus is away", s.CentralSendsFiles(8), stopped(8))
	s.FocusArrives()
	expect(t, "the central sends files at the next visit", s.CentralSendsFiles(9), receive(9))
	expect(t, "the files arrive", s.FilesArrived(9), written(9))
}

func TestASideMachineReceivesOneFileTransferAtATime(t *testing.T) {
	s := sideSharingFiles(t)
	expect(t, "the central sends files", s.CentralSendsFiles(7), receive(7))

	expect(t, "the central sends other files", s.CentralSendsFiles(8), stopped(7), receive(8))
	expect(t, "the files of the first transfer arrive", s.FilesArrived(7))
	expect(t, "the files of the second transfer arrive", s.FilesArrived(8), written(8))
}

func TestASideMachineSendsTheFilesItDescribedUntilItAnswersSomethingElse(t *testing.T) {
	s := sideSharingFiles(t)
	expect(t, "nothing described yet", s.CentralWantsFiles(1), stopped(1))
	describedFiles(t, s, report)
	expect(t, "the central asks for the files", s.CentralWantsFiles(2), sendFiles(2))
	s.FocusArrives()
	s.FocusLeaves()
	expect(t, "nothing new on the clipboard", s.ReadUnchanged(), toCentral{Do: Unchanged})
	expect(t, "the central asks again", s.CentralWantsFiles(3), sendFiles(3))

	ends := []struct {
		name string
		end  func(s *Side)
	}{
		{"the next answer is a text", func(s *Side) { s.FocusArrives(); s.FocusLeaves(); s.ReadGave("a newer text", nil) }},
		{"the next answer is a withdrawal", func(s *Side) { s.FocusArrives(); s.FocusLeaves(); s.ReadGave("", ErrPrivate) }},
		{"the connection is new", func(s *Side) { s.Connected(); s.CentralSays(Text | Files) }},
		{"files are switched off", func(s *Side) { s.Switch(Text) }},
		{"the central stops taking files", func(s *Side) { s.CentralSays(Text) }},
	}
	for _, test := range ends {
		t.Run(test.name, func(t *testing.T) {
			s := sideSharingFiles(t)
			describedFiles(t, s, report)

			test.end(s)

			expect(t, "the central asks for the files", s.CentralWantsFiles(9), stopped(9))
		})
	}
}

func TestASideMachineThatStopsSharingFilesStopsItsTransfers(t *testing.T) {
	roles := []struct {
		name  string
		start func(t *testing.T, s *Side)
	}{
		{"sending", func(t *testing.T, s *Side) {
			describedFiles(t, s, report)
			expect(t, "the central asks for the files", s.CentralWantsFiles(5), sendFiles(5))
		}},
		{"receiving", func(t *testing.T, s *Side) {
			expect(t, "the central sends files", s.CentralSendsFiles(5), receive(5))
		}},
	}
	ends := []struct {
		name string
		end  func(s *Side) []toCentral
		want []toCentral
	}{
		{"files are switched off", func(s *Side) []toCentral { return s.Switch(Text) }, []toCentral{{Do: Announce, Takes: Text}, stopped(5)}},
		{"the central stops taking files", func(s *Side) []toCentral { return s.CentralSays(Text) }, []toCentral{stopped(5)}},
	}
	for _, role := range roles {
		for _, test := range ends {
			t.Run(role.name+", "+test.name, func(t *testing.T) {
				s := sideSharingFiles(t)
				role.start(t, s)

				expect(t, "file sharing ends", test.end(s), test.want...)
				expect(t, "the files arrive anyway", s.FilesArrived(5))
			})
		}
	}
}

func TestASideMachineForgetsTransfersThatEnded(t *testing.T) {
	s := sideSharingFiles(t)
	describedFiles(t, s, report)
	s.CentralWantsFiles(5)
	s.FocusArrives()
	s.CentralSendsFiles(6)

	s.TransferEnded(5)
	s.TransferEnded(6)

	expect(t, "files are switched off", s.Switch(Text), toCentral{Do: Announce, Takes: Text})
}

func TestAnnouncementsCarryEveryKindThatIsOn(t *testing.T) {
	d := NewDesk[string](Text | Files)
	expect(t, "connect A", d.Connected("A"), announce("A", Text|Files))
	expect(t, "A takes text and files", d.Says("A", Text|Files), action{Do: Mark})
	expect(t, "files switched off", d.Switch(Text), announce("A", Text))
	expect(t, "files switched on again", d.Switch(Text|Files), announce("A", Text|Files), action{Do: Mark})

	s := NewSide(Files)
	expect(t, "connect", s.Connected(), toCentral{Do: Announce, Takes: Files})
	expect(t, "text switched on", s.Switch(Text|Files), toCentral{Do: Announce, Takes: Text | Files})
}

func TestKindsAreTheBitsOfFeatures(t *testing.T) {
	var set features.Set
	set.Turn(features.Clipboard, true)
	set.Turn(features.Files, true)
	set.Turn(features.Drag, true)
	set.Turn(8, true)

	if got := KindsIn(set.Value()); got != Text|Files {
		t.Errorf("KindsIn(%#x) = %#x, want text and files %#x", set.Value(), got, Text|Files)
	}
	if got := KindsIn(event.FeatureClipboard); got != Text {
		t.Errorf("KindsIn(the clipboard bit) = %#x, want text %#x", got, Text)
	}
	if got := KindsIn(1 << features.Drag); got != 0 {
		t.Errorf("KindsIn(the drag bit) = %#x, want nothing", got)
	}
}

func TestTheCentralTakesAFreshMarkWhenItStartsToShareAKind(t *testing.T) {
	d := NewDesk[string](Text | Files)
	d.Connected("A")
	d.Connected("B")
	d.Connected("C")

	expect(t, "A takes text", d.Says("A", Text), action{Do: Mark})
	expect(t, "B takes text and files", d.Says("B", Text|Files), action{Do: Mark})
	expect(t, "C takes files", d.Says("C", Files))
	expect(t, "B takes text only", d.Says("B", Text))
	expect(t, "C stops taking files", d.Says("C", none))
	expect(t, "B takes files again", d.Says("B", Text|Files), action{Do: Mark})
}

func TestASideMachineTakesAFreshMarkWhenItStartsToShareAKind(t *testing.T) {
	s := NewSide(Text | Files)
	s.Connected()

	expect(t, "the central takes text", s.CentralSays(Text), toCentral{Do: Mark})
	expect(t, "the central takes files too", s.CentralSays(Text|Files), toCentral{Do: Mark})
	expect(t, "the central says the same again", s.CentralSays(Text|Files))
	expect(t, "files switched off", s.Switch(Text), toCentral{Do: Announce, Takes: Text})
	expect(t, "files switched on", s.Switch(Text|Files), toCentral{Do: Announce, Takes: Text | Files}, toCentral{Do: Mark})
}

func TestSwitchingFilesOffOnTheCentralForgetsAFileCopyAndKeepsAText(t *testing.T) {
	tests := []struct {
		name string
		read func(t *testing.T, d *Desk[string]) []action
		toA  []action
		toB  []action
	}{
		{"a text stays", func(t *testing.T, d *Desk[string]) []action { return readGave(t, d, "x") }, []action{send("A", "x")}, []action{send("B", "x")}},
		{"a file copy is forgotten", func(t *testing.T, d *Desk[string]) []action { return readGaveFiles(t, d, report) }, []action{deliver("A", 1, report)}, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			d := deskSharingFiles(t, "A", "B")
			expect(t, "focus to A", d.FocusOn("A"), read())
			expect(t, "the central read its copy", test.read(t, d), test.toA...)
			d.FilesArrived(1)

			expect(t, "files switched off", d.Switch(Text), announce("A", Text), announce("B", Text))
			expect(t, "files switched on again", d.Switch(Text|Files), announce("A", Text|Files), announce("B", Text|Files), action{Do: Mark})
			expect(t, "focus from A to B", d.FocusOn("B"))
			expect(t, "A had nothing new", d.UnchangedOn("A"), test.toB...)
		})
	}
}

func TestASideMachineLeftWithNothingToShareIsNotWaitedFor(t *testing.T) {
	d := NewDesk[string](Text | Files)
	d.Connected("A")
	expect(t, "A takes files only", d.Says("A", Files), action{Do: Mark})
	d.Connected("B")
	expect(t, "B takes text and files", d.Says("B", Text|Files), action{Do: Mark})
	expect(t, "focus to A", d.FocusOn("A"), read())
	expect(t, "the central read a text, which A does not take", readGave(t, d, "x"))
	expect(t, "focus from A to B", d.FocusOn("B"))

	expect(t, "files switched off, so A shares nothing", d.Switch(Text), announce("A", Text), announce("B", Text), send("B", "x"))
	expect(t, "the answer A sent before the switch", d.UnchangedOn("A"))
}

func TestASideMachineThatNoLongerSharesAnyKindWithTheCentralIsNotWaitedFor(t *testing.T) {
	d := waitingForA(t)

	expect(t, "A takes files only, which the central does not take", d.Says("A", Files), send("B", "x"))
}
