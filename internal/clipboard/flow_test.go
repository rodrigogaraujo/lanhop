package clipboard

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

type action = Action[string]
type toCentral = Action[ToCentral]

const none Kinds = 0

func read() action                           { return action{Do: Read} }
func send(to, text string) action            { return action{Do: Send, To: to, Text: text} }
func write(text string) action               { return action{Do: Write, Text: text} }
func announce(to string, takes Kinds) action { return action{Do: Announce, To: to, Takes: takes} }

func expect[M comparable](t *testing.T, step string, got []Action[M], want ...Action[M]) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s: actions = %+v, want %+v", step, got, want)
	}
}

func deskWith(t *testing.T, sides ...string) *Desk[string] {
	t.Helper()
	d := NewDesk[string](Text)
	for i, side := range sides {
		expect(t, "connect "+side, d.Connected(side), announce(side, Text))
		var startsToTakePart []action
		if i == 0 {
			startsToTakePart = []action{{Do: Mark, Takes: Text}}
		}
		expect(t, side+" says it takes copies", d.Says(side, Text), startsToTakePart...)
	}
	return d
}

func readGave(t *testing.T, d *Desk[string], text string) []action {
	t.Helper()
	actions, err := d.ReadGave(text, nil)
	if err != nil {
		t.Fatalf("ReadGave(%q): %v", text, err)
	}
	return actions
}

func copyOnTheCentralGoesTo(t *testing.T, d *Desk[string], side, text string) {
	t.Helper()
	expect(t, "focus to "+side, d.FocusOn(side), read())
	expect(t, "the central read "+text, readGave(t, d, text), send(side, text))
}

func connectedSide(t *testing.T) *Side {
	t.Helper()
	s := NewSide(Text)
	expect(t, "connect", s.Connected(), toCentral{Do: Announce, Takes: Text})
	expect(t, "the central takes copies", s.CentralSays(Text), toCentral{Do: Mark, Takes: Text})
	s.FocusArrives()
	return s
}

func TestACopyOnTheCentralGoesToTheSideMachineThatGetsFocus(t *testing.T) {
	d := deskWith(t, "A")

	expect(t, "focus to A", d.FocusOn("A"), read())
	expect(t, "the central read its copy", readGave(t, d, "hello"), send("A", "hello"))
}

func TestACopyOnASideMachineIsWrittenOnTheCentralThatHasFocus(t *testing.T) {
	s := connectedSide(t)
	expect(t, "focus leaves the side machine", s.FocusLeaves(), toCentral{Do: Read})
	sent, err := s.ReadGave("from A", nil)
	if err != nil {
		t.Fatalf("ReadGave: %v", err)
	}
	expect(t, "the side machine read its copy", sent, toCentral{Do: Send, Text: "from A"})

	d := deskWith(t, "A")
	d.FocusOn("A")
	expect(t, "the central had nothing new", d.ReadUnchanged())
	expect(t, "focus back on the central", d.FocusOnCentral())
	expect(t, "the copy of A arrives", d.CopyFrom("A", "from A"), write("from A"))
}

func TestTheMachineThatGetsFocusWaitsForTheCentralToReadItsClipboard(t *testing.T) {
	d := deskWith(t, "A", "B")
	copyOnTheCentralGoesTo(t, d, "A", "old")
	d.FocusOnCentral()
	d.UnchangedOn("A")

	expect(t, "focus to B", d.FocusOn("B"), read())
	expect(t, "the central had nothing new", d.ReadUnchanged(), send("B", "old"))
}

func TestTheMachineThatGetsFocusWaitsForTheAnswerOfTheOneThatLostIt(t *testing.T) {
	d := deskWith(t, "A", "B")
	copyOnTheCentralGoesTo(t, d, "A", "old")

	expect(t, "focus from A to B", d.FocusOn("B"))
	expect(t, "A had nothing new", d.UnchangedOn("A"), send("B", "old"))
}

func TestACopyGoesStraightFromOneSideMachineToAnother(t *testing.T) {
	d := deskWith(t, "A", "B")
	d.FocusOn("A")
	d.ReadUnchanged()

	expect(t, "focus from A to B", d.FocusOn("B"))
	expect(t, "the copy of A arrives", d.CopyFrom("A", "x"), send("B", "x"))
}

func TestACopyFromASideMachineWaitsInTheCentralUntilFocusComesBack(t *testing.T) {
	d := deskWith(t, "A", "B")
	d.FocusOn("A")
	d.ReadUnchanged()
	d.FocusOn("B")

	expect(t, "the copy of A arrives while B has focus", d.CopyFrom("A", "x"), send("B", "x"))
	expect(t, "focus back on the central", d.FocusOnCentral())
	expect(t, "B had nothing new", d.UnchangedOn("B"), write("x"))
}

func TestACopyThatVisitedTheCentralGoesOnToTheNextSideMachine(t *testing.T) {
	d := deskWith(t, "A", "B")
	d.FocusOn("A")
	d.ReadUnchanged()
	d.FocusOnCentral()
	expect(t, "the copy of A arrives at the central", d.CopyFrom("A", "x"), write("x"))

	expect(t, "focus to B", d.FocusOn("B"), read())
	expect(t, "the central still holds the copy of A", readGave(t, d, "x"), send("B", "x"))
}

func TestANewerCopyOnTheCentralWinsOverOneThatPassedThrough(t *testing.T) {
	d := deskWith(t, "A", "B")
	d.FocusOn("A")
	d.ReadUnchanged()
	d.FocusOnCentral()
	d.CopyFrom("A", "x")

	expect(t, "focus to B", d.FocusOn("B"), read())
	expect(t, "the central had a newer copy", readGave(t, d, "z"), send("B", "z"))
}

func TestACopyReachesOnlyTheMachinesThatGetFocus(t *testing.T) {
	d := deskWith(t, "A", "B")
	copyOnTheCentralGoesTo(t, d, "A", "secret")

	expect(t, "focus back on the central", d.FocusOnCentral())
	expect(t, "A had nothing new", d.UnchangedOn("A"))
	expect(t, "focus to A again", d.FocusOn("A"), read())
	expect(t, "the central had nothing new", d.ReadUnchanged())
	expect(t, "focus to B, later", d.FocusOn("B"))
	expect(t, "A had nothing new", d.UnchangedOn("A"), send("B", "secret"))
}

func TestAWithdrawalFromTheMachineThatLostFocusArrivesBeforeTheNextOneGetsTheLastCopy(t *testing.T) {
	d := deskWith(t, "A", "B")
	copyOnTheCentralGoesTo(t, d, "A", "password")

	expect(t, "focus from A to B", d.FocusOn("B"))
	expect(t, "the password manager of A emptied its clipboard", d.CopyFrom("A", ""))
	expect(t, "focus back on the central", d.FocusOnCentral())
	expect(t, "B had nothing new", d.UnchangedOn("B"))
}

func TestAMachineNeverGetsBackItsOwnCopy(t *testing.T) {
	d := deskWith(t, "A", "B")
	d.FocusOn("A")
	d.ReadUnchanged()
	d.FocusOn("B")
	d.CopyFrom("A", "x")

	expect(t, "focus back to A", d.FocusOn("A"))
	expect(t, "B had nothing new", d.UnchangedOn("B"))
	expect(t, "the same copy of A arrives late", d.CopyFrom("A", "x"))
}

func TestACopyEqualToTheLastOneIsNotACopyAgain(t *testing.T) {
	d := deskWith(t, "A", "B")
	copyOnTheCentralGoesTo(t, d, "A", "x")
	d.FocusOn("B")

	expect(t, "A copied the same text", d.CopyFrom("A", "x"), send("B", "x"))
	expect(t, "focus to the central", d.FocusOnCentral())
	expect(t, "B copied the same text", d.CopyFrom("B", "x"))
	expect(t, "focus to A", d.FocusOn("A"), read())
	expect(t, "the central copied the same text", readGave(t, d, "x"))
}

func TestACopyThatIsNotCarriedWithdrawsTheLastOne(t *testing.T) {
	notText := errors.New("the clipboard holds an image")
	tests := []struct {
		name    string
		text    string
		readErr error
		want    error
	}{
		{"an image", "", notText, notText},
		{"an emptied clipboard", "", nil, ErrEmpty},
		{"a text over the limit", strings.Repeat("x", Limit+1), nil, ErrTooLarge},
		{"invalid UTF-8", "\xff", nil, ErrNotText},
		{"a password marked private", "", ErrPrivate, ErrPrivate},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			d := deskWith(t, "A", "B")
			copyOnTheCentralGoesTo(t, d, "A", "earlier text")
			d.FocusOnCentral()
			d.UnchangedOn("A")

			expect(t, "focus to B", d.FocusOn("B"), read())
			actions, err := d.ReadGave(test.text, test.readErr)

			if !errors.Is(err, test.want) {
				t.Errorf("error = %v, want %v", err, test.want)
			}
			expect(t, "B keeps what it held", actions)
		})
	}
}

func TestASideMachineWithdrawsWhatItCannotSend(t *testing.T) {
	s := connectedSide(t)
	s.FocusLeaves()

	sent, err := s.ReadGave("", nil)

	if !errors.Is(err, ErrEmpty) {
		t.Errorf("error = %v, want %v", err, ErrEmpty)
	}
	expect(t, "an emptied clipboard", sent, toCentral{Do: Withdraw})
}

func TestASideMachineWithdrawsACopyMarkedPrivate(t *testing.T) {
	s := connectedSide(t)
	s.FocusLeaves()

	sent, err := s.ReadGave("", ErrPrivate)

	if !errors.Is(err, ErrPrivate) {
		t.Errorf("error = %v, want %v", err, ErrPrivate)
	}
	expect(t, "a password marked private", sent, toCentral{Do: Withdraw})
}

func TestASideMachineSaysWhenItsClipboardDidNotChange(t *testing.T) {
	s := connectedSide(t)

	expect(t, "nothing new", s.ReadUnchanged(), toCentral{Do: Unchanged})
	s.Switch(none)
	expect(t, "nothing new while off", s.ReadUnchanged())
}

func TestACentralWhoseWriteWasSkippedSendsItsOwnCopyOnTheNextLeave(t *testing.T) {
	d := deskWith(t, "A", "B")
	d.FocusOn("A")
	d.ReadUnchanged()
	d.FocusOnCentral()

	expect(t, "the copy of A arrives", d.CopyFrom("A", "from A"), write("from A"))
	expect(t, "focus to B", d.FocusOn("B"), read())
	expect(t, "the write was skipped, so the central holds its own copy", readGave(t, d, "mine"), send("B", "mine"))
}

func waitingForA(t *testing.T) *Desk[string] {
	t.Helper()
	d := deskWith(t, "A", "B")
	copyOnTheCentralGoesTo(t, d, "A", "x")
	d.FocusOnCentral()
	d.UnchangedOn("A")
	expect(t, "focus to A", d.FocusOn("A"), read())
	d.ReadUnchanged()
	expect(t, "focus from A to B", d.FocusOn("B"))
	return d
}

func TestASideMachineThatDoesNotAnswerCountsAsAWithdrawal(t *testing.T) {
	ends := map[string]func(d *Desk[string]) []action{
		"the central gives up":   func(d *Desk[string]) []action { return d.GaveUpOn("A") },
		"the connection is lost": func(d *Desk[string]) []action { return d.Lost("A") },
	}
	for name, end := range ends {
		t.Run(name, func(t *testing.T) {
			d := waitingForA(t)

			expect(t, "the wait for A ends", end(d))
			expect(t, "focus to the central", d.FocusOnCentral())
			expect(t, "B had nothing new", d.UnchangedOn("B"))
		})
	}
}

func TestAnAnswerThatComesAfterTheCentralGaveUpStillCounts(t *testing.T) {
	d := waitingForA(t)
	d.GaveUpOn("A")

	expect(t, "the copy of A arrives late", d.CopyFrom("A", "late copy"), send("B", "late copy"))
}

func TestGivingUpOnAMachineThatWasNotWaitedForChangesNothing(t *testing.T) {
	d := deskWith(t, "A", "B")
	copyOnTheCentralGoesTo(t, d, "A", "x")

	expect(t, "give up on B", d.GaveUpOn("B"))
	expect(t, "B is lost", d.Lost("B"))
	expect(t, "focus to the central", d.FocusOnCentral())
	expect(t, "A had nothing new", d.UnchangedOn("A"))
	d.Connected("C")
	d.Says("C", Text)
	expect(t, "focus to C", d.FocusOn("C"), read())
	expect(t, "the central had nothing new", d.ReadUnchanged(), send("C", "x"))
}

func TestASideMachineThatSwitchesOffInsteadOfAnsweringIsNotWaitedFor(t *testing.T) {
	d := waitingForA(t)

	expect(t, "A says it no longer takes copies", d.Says("A", none), send("B", "x"))
}

func TestASideMachineThatIsOffNeitherReadsNorWrites(t *testing.T) {
	s := NewSide(none)
	expect(t, "connect", s.Connected(), toCentral{Do: Announce, Takes: none})
	s.CentralSays(Text)

	expect(t, "focus leaves", s.FocusLeaves())
	expect(t, "a copy arrives", s.CopyFromCentral("x"))
}

func TestTheCentralSendsNothingToASideMachineThatIsOff(t *testing.T) {
	d := deskWith(t, "A", "B")
	expect(t, "B switches off", d.Says("B", none))
	copyOnTheCentralGoesTo(t, d, "A", "x")
	d.UnchangedOn("A")

	expect(t, "focus to B", d.FocusOn("B"))
	expect(t, "A had nothing new", d.UnchangedOn("A"))
	expect(t, "B sends a copy anyway", d.CopyFrom("B", "y"))
	expect(t, "focus to the central, which made the copy", d.FocusOnCentral())
}

func TestACentralThatIsOffMovesNoCopy(t *testing.T) {
	d := NewDesk[string](none)
	expect(t, "connect A", d.Connected("A"), announce("A", none))
	d.Says("A", Text)

	expect(t, "focus to A", d.FocusOn("A"))
	expect(t, "a copy of A", d.CopyFrom("A", "x"))
	actions, err := d.ReadGave("y", nil)
	if err != nil {
		t.Fatalf("ReadGave: %v", err)
	}
	expect(t, "a read", actions)
	expect(t, "focus to the central", d.FocusOnCentral())
}

func TestSwitchingTheCentralOffTellsEverySideMachineAndForgetsTheLastCopy(t *testing.T) {
	d := deskWith(t, "A", "B", "C")
	d.Lost("B")
	copyOnTheCentralGoesTo(t, d, "A", "x")
	d.FocusOnCentral()

	expect(t, "switch off", d.Switch(none), announce("A", none), announce("C", none))
	expect(t, "switch off again", d.Switch(none))
	expect(t, "switch on", d.Switch(Text), announce("A", Text), announce("C", Text), action{Do: Mark, Takes: Text})
	expect(t, "focus to C", d.FocusOn("C"), read())
	expect(t, "the central had nothing new", d.ReadUnchanged())
}

func TestSwitchingASideMachineTellsTheCentral(t *testing.T) {
	s := connectedSide(t)

	expect(t, "switch off", s.Switch(none), toCentral{Do: Announce, Takes: none})
	expect(t, "switch off again", s.Switch(none))
	expect(t, "focus leaves while off", s.FocusLeaves())
	expect(t, "switch on", s.Switch(Text), toCentral{Do: Announce, Takes: Text}, toCentral{Do: Mark, Takes: Text})
	expect(t, "focus leaves while on", s.FocusLeaves(), toCentral{Do: Read})
}

func TestASideMachineOnTheOldVersionGetsNoCopyAndIsNotWaitedFor(t *testing.T) {
	d := deskWith(t, "B")
	d.Connected("old")

	expect(t, "focus to the old side machine", d.FocusOn("old"), read())
	expect(t, "the central read its copy", readGave(t, d, "x"))
	expect(t, "focus to B", d.FocusOn("B"), send("B", "x"))
	expect(t, "frames from the old side machine", d.CopyFrom("old", "y"))
}

func TestASideMachineConnectedToACentralOnTheOldVersionReadsNothing(t *testing.T) {
	s := NewSide(Text)
	s.Connected()

	expect(t, "focus leaves", s.FocusLeaves())
	expect(t, "nothing new", s.ReadUnchanged())
	actions, err := s.ReadGave("x", nil)
	if err != nil {
		t.Fatalf("ReadGave: %v", err)
	}
	expect(t, "a read", actions)
}

func TestASideMachineThatReconnectsGetsTheLastCopyAgain(t *testing.T) {
	s := connectedSide(t)
	expect(t, "reconnect", s.Connected(), toCentral{Do: Announce, Takes: Text})
	expect(t, "focus leaves before the central says it takes copies", s.FocusLeaves())

	d := deskWith(t, "A")
	copyOnTheCentralGoesTo(t, d, "A", "x")
	d.FocusOnCentral()
	d.UnchangedOn("A")
	d.Lost("A")
	expect(t, "A connects again", d.Connected("A"), announce("A", Text))
	expect(t, "A says it takes copies", d.Says("A", Text), action{Do: Mark, Takes: Text})
	expect(t, "focus to A", d.FocusOn("A"), read())
	expect(t, "the central had nothing new", d.ReadUnchanged(), send("A", "x"))
}

func TestASideMachineThatSwitchesOnWhileItHasFocusGetsTheLastCopy(t *testing.T) {
	d := deskWith(t, "A", "B")
	d.Says("B", none)
	copyOnTheCentralGoesTo(t, d, "A", "x")
	d.FocusOn("B")
	d.UnchangedOn("A")

	expect(t, "B switches on", d.Says("B", Text), send("B", "x"))
}

func TestCopiesFromMachinesThatAreNotConnectedAreIgnored(t *testing.T) {
	d := deskWith(t, "A")

	expect(t, "a copy from a stranger", d.CopyFrom("stranger", "x"))
	expect(t, "a stranger says it takes copies", d.Says("stranger", Text))
	expect(t, "focus to A", d.FocusOn("A"), read())
}

func TestFocusThatDoesNotMoveChangesNothing(t *testing.T) {
	d := deskWith(t, "A")

	expect(t, "focus on the central it already has", d.FocusOnCentral())
	expect(t, "focus to A", d.FocusOn("A"), read())
	expect(t, "focus to A again", d.FocusOn("A"))
}

func TestASideMachineWritesWhatTheCentralSends(t *testing.T) {
	s := connectedSide(t)

	expect(t, "a copy arrives", s.CopyFromCentral("x"), toCentral{Do: Write, Text: "x"})
	expect(t, "a withdrawal arrives", s.CopyFromCentral(""))
}

func TestASideMachineWritesACopyOnlyWhileItHasFocus(t *testing.T) {
	s := connectedSide(t)
	s.FocusLeaves()

	expect(t, "a copy completes after focus left", s.CopyFromCentral("late"))
	s.FocusArrives()
	expect(t, "a copy arrives with focus", s.CopyFromCentral("now"), toCentral{Do: Write, Text: "now"})
	s.Connected()
	expect(t, "a copy arrives on a new connection before focus", s.CopyFromCentral("early"))
}

func aSendToAIsCutShortAfterALateAnswerOfA(t *testing.T, d *Desk[string], lateAnswer func(d *Desk[string]) []action) {
	t.Helper()
	d.FocusOn("A")
	d.ReadUnchanged()
	expect(t, "focus from A to B", d.FocusOn("B"))
	expect(t, "the central gives up on A", d.GaveUpOn("A"))
	expect(t, "focus back to A", d.FocusOn("A"))
	expect(t, "the copy of B arrives", d.CopyFrom("B", "t2"), send("A", "t2"))
	expect(t, "the late answer of A arrives while t2 is on its way", lateAnswer(d))
	d.SendStopped("A")
	expect(t, "focus leaves A before t2 has arrived", d.FocusOn("C"))
}

func TestAStoppedSendDoesNotUndoANewerCopyMadeOnThatSideMachine(t *testing.T) {
	d := deskWith(t, "A", "B", "C")
	aSendToAIsCutShortAfterALateAnswerOfA(t, d, func(d *Desk[string]) []action { return d.CopyFrom("A", "t3") })

	expect(t, "A had nothing new", d.UnchangedOn("A"), send("C", "t3"))
	expect(t, "focus back to A", d.FocusOn("A"))
	expect(t, "C had nothing new", d.UnchangedOn("C"))
}

func TestACopyCutShortByALeaveIsNotHeld(t *testing.T) {
	d := deskWith(t, "A", "B")
	copyOnTheCentralGoesTo(t, d, "A", "big copy")
	d.SendStopped("A")
	d.SendStopped("stranger")
	d.FocusOn("B")
	d.UnchangedOn("A")

	expect(t, "focus back on A", d.FocusOn("A"))
	expect(t, "B had nothing new", d.UnchangedOn("B"), send("A", "big copy"))
}

func TestActionsHaveNames(t *testing.T) {
	names := map[Do]string{
		Announce:     "announce",
		Send:         "send",
		Withdraw:     "withdraw",
		Unchanged:    "unchanged",
		Write:        "write",
		Read:         "read",
		Mark:         "mark",
		Describe:     "describe",
		Fetch:        "fetch",
		Deliver:      "deliver",
		SendFiles:    "send files",
		ReceiveFiles: "receive files",
		WriteFiles:   "write files",
		Stop:         "stop",
		0:            "nothing",
	}
	for do, want := range names {
		if got := do.String(); got != want {
			t.Errorf("Do(%d) = %q, want %q", do, got, want)
		}
	}
}

func TestASideMachineLeftTwiceIsWaitedForTwice(t *testing.T) {
	d := deskWith(t, "A", "B", "C")
	copyOnTheCentralGoesTo(t, d, "A", "x")
	d.FocusOn("B")
	d.FocusOn("A")

	expect(t, "focus from A to C, A left twice", d.FocusOn("C"))
	expect(t, "A answers its first leave", d.UnchangedOn("A"))
	expect(t, "B answers", d.UnchangedOn("B"))
	expect(t, "A withdraws in answer to its second leave", d.WithdrawalFrom("A"))
	expect(t, "focus to the central", d.FocusOnCentral())
	expect(t, "C had nothing new", d.UnchangedOn("C"))
}

func TestTheCentralLeftTwiceWaitsForBothReads(t *testing.T) {
	d := deskWith(t, "A", "B")
	expect(t, "focus to A", d.FocusOn("A"), read())
	d.FocusOnCentral()
	d.UnchangedOn("A")
	expect(t, "focus to B before the first read ends", d.FocusOn("B"), read())

	expect(t, "the first read gives a text", readGave(t, d, "first"))
	actions, err := d.ReadGave("", ErrPrivate)
	if !errors.Is(err, ErrPrivate) {
		t.Errorf("error = %v, want %v", err, ErrPrivate)
	}
	expect(t, "the second read gives a private copy", actions)
}

func TestLosingTheSideMachineThatHasFocusLeavesNothingToWaitFor(t *testing.T) {
	d := deskWith(t, "A", "B")
	copyOnTheCentralGoesTo(t, d, "A", "x")

	expect(t, "A is lost while it has focus", d.Lost("A"))
	expect(t, "focus goes back to the central", d.FocusOnCentral())
	expect(t, "focus to B", d.FocusOn("B"), read())
	expect(t, "the central had nothing new", d.ReadUnchanged(), send("B", "x"))
}

func TestASideMachineThatReconnectsWithoutBeingLostIsNoLongerWaitedFor(t *testing.T) {
	d := deskWith(t, "A", "B")
	d.FocusOn("A")
	d.ReadUnchanged()
	d.FocusOn("B")

	expect(t, "A connects again", d.Connected("A"), announce("A", Text))
	expect(t, "focus to the central", d.FocusOnCentral())
	expect(t, "B had nothing new", d.UnchangedOn("B"))
	expect(t, "focus to B", d.FocusOn("B"), read())
	expect(t, "the central read its copy", readGave(t, d, "z"), send("B", "z"))
}

func TestASideMachineThatReconnectsBeforeAnsweringCountsAsAWithdrawal(t *testing.T) {
	d := waitingForA(t)

	expect(t, "A connects again", d.Connected("A"), announce("A", Text))
	expect(t, "focus to the central", d.FocusOnCentral())
	expect(t, "B had nothing new", d.UnchangedOn("B"))
}

func TestASideMachineThatStartsToTakePartWaitsForTheAnswersStillDue(t *testing.T) {
	d := deskWith(t, "A", "B", "C")
	d.Says("C", none)
	copyOnTheCentralGoesTo(t, d, "A", "x")
	d.FocusOn("C")

	expect(t, "C starts to take part while A has not answered", d.Says("C", Text))
	expect(t, "A had nothing new", d.UnchangedOn("A"), send("C", "x"))
}

func TestTheCentralDoesNotReadWhenNoSideMachineTakesPart(t *testing.T) {
	d := NewDesk[string](Text)
	d.Connected("old")

	expect(t, "focus to the old side machine", d.FocusOn("old"))
	expect(t, "focus back on the central", d.FocusOnCentral())
}

func TestTheCentralTakesAFreshMarkWhenItStartsToTakePart(t *testing.T) {
	d := NewDesk[string](none)
	d.Connected("A")

	expect(t, "A takes copies while the central is off", d.Says("A", Text))
	expect(t, "switch on", d.Switch(Text), announce("A", Text), action{Do: Mark, Takes: Text})
	expect(t, "A stops taking copies", d.Says("A", none))
	expect(t, "A takes copies again", d.Says("A", Text), action{Do: Mark, Takes: Text})
	d.Connected("B")
	expect(t, "B takes copies too", d.Says("B", Text))
}

func TestASideMachineTakesAFreshMarkWhenItStartsToTakePart(t *testing.T) {
	s := NewSide(Text)
	s.Connected()

	expect(t, "the central takes copies", s.CentralSays(Text), toCentral{Do: Mark, Takes: Text})
	expect(t, "the central says it again", s.CentralSays(Text))
	expect(t, "the central stops", s.CentralSays(none))
	expect(t, "switch off", s.Switch(none), toCentral{Do: Announce, Takes: none})
	expect(t, "the central takes copies while this side is off", s.CentralSays(Text))
	expect(t, "switch on", s.Switch(Text), toCentral{Do: Announce, Takes: Text}, toCentral{Do: Mark, Takes: Text})
}

func TestASideMachineDropsACopyThatArrivesAfterTheCentralStopped(t *testing.T) {
	s := connectedSide(t)
	s.CentralSays(none)

	expect(t, "a copy still on its way", s.CopyFromCentral("x"))
}

func TestALateAnswerDoesNotStandForTheAnswerToALaterLeave(t *testing.T) {
	d := waitingForA(t)
	expect(t, "the central gives up on A", d.GaveUpOn("A"))
	d.FocusOn("A")
	d.UnchangedOn("B")
	d.FocusOn("B")

	expect(t, "the late answer to the first leave", d.CopyFrom("A", "late copy"))
	expect(t, "the answer to the second leave", d.WithdrawalFrom("A"))
	expect(t, "focus to the central", d.FocusOnCentral())
	expect(t, "B had nothing new", d.UnchangedOn("B"))
}

func TestGivingUpWritesOffOnlyOneAnswer(t *testing.T) {
	d := deskWith(t, "A", "B")
	copyOnTheCentralGoesTo(t, d, "A", "x")
	d.FocusOn("B")
	d.FocusOn("A")
	d.FocusOn("B")
	d.UnchangedOn("B")

	expect(t, "the timer of the first leave of A runs out", d.GaveUpOn("A"))
	expect(t, "the late answer to the first leave", d.UnchangedOn("A"))
	expect(t, "the answer to the second leave", d.CopyFrom("A", "y"), send("B", "y"))
}

func TestResultsOnTheirWayWhenTheCentralSwitchedOffAreDropped(t *testing.T) {
	d := deskWith(t, "A", "B")
	expect(t, "focus to A", d.FocusOn("A"), read())
	d.FocusOnCentral()
	d.Switch(none)
	d.Switch(Text)
	expect(t, "focus to B", d.FocusOn("B"), read())

	expect(t, "the read from before the switch", readGave(t, d, "secret"))
	expect(t, "the answer of A from before the switch", d.CopyFrom("A", "old copy"))
	expect(t, "the read after the switch", d.ReadUnchanged())
	expect(t, "focus to the central", d.FocusOnCentral())
	expect(t, "B had nothing new", d.UnchangedOn("B"))
}
