package sharer

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/rodrigogaraujo/lanhop/internal/event"
)

var errCrossingRefused = errors.New("the origin could not be told that its drag leaves")

func TestACrossingThatTheWiringRefusesLeavesTheDragAndTheButtonWithItsOrigin(t *testing.T) {
	s := newSharing(t, desk, laptop)
	s.refuses = true
	s.endsShown = true
	s.desk.Store(drawn(t, "me desk laptop"))
	s.startWithAScreen(ownDesktop)
	s.connect(desk)
	s.connectTakingDrags(laptop)

	s.focusOn(2)
	s.moves(400, 0)
	s.holdsTheButton()
	s.dragStartsOn("desk", 3, noDock)
	s.pushes(1, 0, 3*dragReportsToCross)

	journal := s.journal.read()
	if !slices.Contains(journal, dragged("desk", "desk", "laptop", 3)) {
		t.Fatalf("the crossing was never asked for, the journal is:\n%s", strings.Join(journal, "\n"))
	}
	if !slices.Contains(journal, dragEnds(Drag{Origin: "desk", Files: 3})) {
		t.Errorf("the refused drag did not end, the journal is:\n%s", strings.Join(journal, "\n"))
	}
	for _, entry := range journal {
		if strings.HasPrefix(entry, "laptop gets") || entry == gets(desk, event.Leave())[0] || entry == gets(desk, release(event.ButtonLeft))[0] {
			t.Errorf("a refused crossing led to %q, the journal is:\n%s", entry, strings.Join(journal, "\n"))
		}
	}
	if crossings := strings.Count(strings.Join(journal, "\n"), dragged("desk", "desk", "laptop", 3)); crossings != 1 {
		t.Errorf("the crossing was asked for %d times, want once, as the held button blocks the edge once the drag ended", crossings)
	}

	s.releasesTheButton()
	s.eventually("desk getting the release of the button", func() bool {
		return s.journal.has(gets(desk, release(event.ButtonLeft))[0])
	})
}
