package clipboard

import (
	"fmt"
	"slices"
	"testing"
)

type wiredSide struct {
	machine     *Side
	nextText    string
	nextFiles   FileCopy
	holdsReads  bool
	readsHeld   int
	holdsFrames bool
	inTransit   []toCentral
	written     []string
	delivered   int
}

type wiredDesk struct {
	desk    *Desk[string]
	sides   map[string]*wiredSide
	focused string
}

func wire(kinds Kinds, names ...string) *wiredDesk {
	w := &wiredDesk{desk: NewDesk[string](kinds), sides: make(map[string]*wiredSide)}
	for _, name := range names {
		side := &wiredSide{machine: NewSide(kinds)}
		w.sides[name] = side
		fromCentral := w.desk.Connected(name)
		w.sideDoes(name, side.machine.Connected())
		w.centralDoes(fromCentral)
	}
	return w
}

func (w *wiredDesk) centralDoes(actions []action) {
	for _, a := range actions {
		switch a.Do {
		case Announce:
			w.sideDoes(a.To, w.sides[a.To].machine.CentralSays(a.Takes))
		case Send:
			w.sideDoes(a.To, w.sides[a.To].machine.CopyFromCentral(a.Text))
		case Deliver:
			w.sides[a.To].delivered++
		case Read:
			w.centralDoes(w.desk.ReadUnchanged())
		}
	}
}

func (w *wiredDesk) sideDoes(name string, actions []toCentral) {
	side := w.sides[name]
	for _, a := range actions {
		switch a.Do {
		case Read:
			side.readsHeld++
			if !side.holdsReads {
				w.endARead(name)
			}
		case Write:
			side.written = append(side.written, a.Text)
		case Announce, Unchanged, Withdraw, Send, Describe:
			if side.holdsFrames {
				side.inTransit = append(side.inTransit, a)
			} else {
				w.centralHears(name, a)
			}
		}
	}
}

func (w *wiredDesk) centralHears(name string, a toCentral) {
	switch a.Do {
	case Announce:
		w.centralDoes(w.desk.Says(name, a.Takes))
	case Unchanged:
		w.centralDoes(w.desk.UnchangedOn(name))
	case Withdraw:
		w.centralDoes(w.desk.WithdrawalFrom(name))
	case Send:
		w.centralDoes(w.desk.CopyFrom(name, a.Text))
	case Describe:
		actions, _ := w.desk.FileCopyFrom(name, a.FileCopy)
		w.centralDoes(actions)
	}
}

func (w *wiredDesk) endARead(name string) {
	side := w.sides[name]
	side.readsHeld--
	var answer []toCentral
	switch {
	case side.nextFiles.FilesAndFolders > 0:
		answer, _ = side.machine.ReadGaveFiles(side.nextFiles, nil)
	case side.nextText != "":
		answer, _ = side.machine.ReadGave(side.nextText, nil)
	default:
		answer = side.machine.ReadUnchanged()
	}
	side.nextText, side.nextFiles = "", FileCopy{}
	w.sideDoes(name, answer)
}

func (w *wiredDesk) letReadsOf(name string) {
	side := w.sides[name]
	side.holdsReads = false
	for side.readsHeld > 0 {
		w.endARead(name)
	}
}

func (w *wiredDesk) deliverFramesOf(name string) {
	side := w.sides[name]
	side.holdsFrames = false
	inTransit := side.inTransit
	side.inTransit = nil
	for _, a := range inTransit {
		w.centralHears(name, a)
	}
}

func (w *wiredDesk) focusOn(next string) {
	left := w.focused
	w.focused = next
	var counted []action
	if next == "" {
		counted = w.desk.FocusOnCentral()
	} else {
		w.sides[next].machine.FocusArrives()
		counted = w.desk.FocusOn(next)
	}
	w.centralDoes(counted)
	if left != "" {
		w.sideDoes(left, w.sides[left].machine.FocusLeaves())
	}
}

func (w *wiredDesk) sideSwitches(name string, on Kinds) {
	w.sideDoes(name, w.sides[name].machine.Switch(on))
}

func (w *wiredDesk) textsCopiedOnAReachB(t *testing.T, rounds int) {
	t.Helper()
	before := slices.Clone(w.sides["B"].written)
	for round := 1; round <= rounds; round++ {
		text := fmt.Sprintf("copy %d of A", round)
		w.focusOn("A")
		w.sides["A"].nextText = text
		w.focusOn("B")
		before = append(before, text)
		if got := w.sides["B"].written; !slices.Equal(got, before) {
			t.Fatalf("round %d: B has had %q written, want %q", round, got, before)
		}
	}
}

func (w *wiredDesk) fileCopiesMadeOnAReachB(t *testing.T, rounds int) {
	t.Helper()
	deliveredBefore := w.sides["B"].delivered
	for round := 1; round <= rounds; round++ {
		w.focusOn("A")
		w.sides["A"].nextFiles = FileCopy{FilesAndFolders: round, Bytes: int64(round) << 20}
		w.focusOn("B")
		if got := w.sides["B"].delivered - deliveredBefore; got != round {
			t.Fatalf("round %d: %d file copies were delivered to B, want %d", round, got, round)
		}
	}
	if written := w.sides["B"].written; len(written) > 0 {
		t.Errorf("B has had %q written, want no text", written)
	}
}

func TestAnswersStayInStepAfterTheCentralGaveUpOnASlowReadAndWasSwitchedOff(t *testing.T) {
	w := wire(Text, "A", "B")
	w.focusOn("A")
	w.sides["A"].nextText = "copied on A before the central was switched off"
	w.sides["A"].holdsReads = true
	w.focusOn("B")

	w.centralDoes(w.desk.GaveUpOn("A"))
	w.centralDoes(w.desk.Switch(none))
	w.letReadsOf("A")
	w.centralDoes(w.desk.Switch(Text))

	w.textsCopiedOnAReachB(t, 3)
}

func TestAnswersStayInStepAfterTheCentralWasSwitchedOffDuringASlowReadOfFiles(t *testing.T) {
	w := wire(Text|Files, "A", "B")
	w.focusOn("A")
	w.sides["A"].nextFiles = report
	w.sides["A"].holdsReads = true
	w.focusOn("B")

	w.centralDoes(w.desk.Switch(none))
	w.letReadsOf("A")
	w.centralDoes(w.desk.Switch(Text | Files))

	w.fileCopiesMadeOnAReachB(t, 3)
}

func TestAnswersStayInStepAfterTheCentralSwitchedOffTheOnlyKindASideMachineShared(t *testing.T) {
	w := wire(Text|Files, "A", "B")
	w.sideSwitches("A", Files)
	w.focusOn("A")
	w.sides["A"].nextFiles = report
	w.sides["A"].holdsReads = true
	w.focusOn("B")

	w.centralDoes(w.desk.Switch(Text))
	w.letReadsOf("A")
	w.centralDoes(w.desk.Switch(Text | Files))

	w.fileCopiesMadeOnAReachB(t, 3)
}

func TestAnswersStayInStepAfterASideMachineSwitchedOffDuringASlowRead(t *testing.T) {
	w := wire(Text, "A", "B")
	w.focusOn("A")
	w.sides["A"].nextText = "copied on A before it was switched off"
	w.sides["A"].holdsReads = true
	w.focusOn("B")

	w.sideSwitches("A", none)
	w.letReadsOf("A")
	w.sideSwitches("A", Text)

	w.textsCopiedOnAReachB(t, 3)
}

func TestAnswersStayInStepWhileASideMachineDoesNotTakePart(t *testing.T) {
	w := wire(Text, "A", "B")
	w.sideSwitches("A", none)
	w.focusOn("A")
	w.focusOn("B")
	w.focusOn("A")
	w.focusOn("B")
	w.sideSwitches("A", Text)

	w.textsCopiedOnAReachB(t, 3)
}

func TestAnAnswerToALeaveCountedWhileTheSideMachineSeemedOffIsNotTakenForTheNextOne(t *testing.T) {
	w := wire(Text, "A", "B")
	w.sideSwitches("A", none)
	w.sides["A"].holdsFrames = true
	w.sideSwitches("A", Text)
	w.focusOn("A")
	w.sides["A"].nextText = "read while the central still thought A was off"
	w.sides["A"].holdsReads = true
	w.focusOn("B")
	w.deliverFramesOf("A")
	w.focusOn("A")
	w.focusOn("B")

	w.endARead("A")
	w.sides["A"].nextText = "read at the next leave"
	w.letReadsOf("A")

	if got, want := w.sides["B"].written, []string{"read at the next leave"}; !slices.Equal(got, want) {
		t.Errorf("B has had %q written, want %q", got, want)
	}
}

func TestAnAnswerOfASideMachineThatSwitchedOffAndOnDuringASlowReadIsNotTakenForTheNextLeave(t *testing.T) {
	w := wire(Text, "A", "B")
	w.focusOn("A")
	w.sides["A"].nextText = "read before A switched off and on"
	w.sides["A"].holdsReads = true
	w.focusOn("B")
	w.sideSwitches("A", none)
	w.sideSwitches("A", Text)
	w.focusOn("A")
	w.focusOn("B")

	w.endARead("A")
	w.sides["A"].nextText = "read at the next leave"
	w.letReadsOf("A")

	if got, want := w.sides["B"].written, []string{"read at the next leave"}; !slices.Equal(got, want) {
		t.Errorf("B has had %q written, want %q", got, want)
	}
}
