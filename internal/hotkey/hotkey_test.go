package hotkey

import (
	"testing"

	"github.com/rodrigogaraujo/lanhop/internal/event"
)

const (
	keyDigit2 uint16 = 3
	keyDigit3 uint16 = 4
	keyDigit0 uint16 = 11
)

func press(code uint16) event.Event   { return event.Key(code, event.KeyPressed) }
func release(code uint16) event.Event { return event.Key(code, event.KeyReleased) }
func repeat(code uint16) event.Event  { return event.Key(code, event.KeyRepeated) }

func newDetector(t *testing.T, modifiers string) *Detector {
	t.Helper()
	detector, err := New(modifiers)
	if err != nil {
		t.Fatalf("New(%q): %v", modifiers, err)
	}
	return detector
}

func feed(detector *Detector, events ...event.Event) []Result {
	results := make([]Result, len(events))
	for i, e := range events {
		results[i] = detector.Feed(e)
	}
	return results
}

func switches(results []Result) []int {
	var targets []int
	for _, result := range results {
		if result.Switch {
			targets = append(targets, result.Target)
		}
	}
	return targets
}

func TestHotkeySwitchesWhenTheLastKeyIsReleased(t *testing.T) {
	detector := newDetector(t, DefaultModifiers)
	results := feed(detector,
		press(keyLeftCtrl), press(keyLeftAlt), press(keyDigit2),
		release(keyDigit2), release(keyLeftAlt), release(keyLeftCtrl),
	)

	for i, result := range results[:5] {
		if result.Switch {
			t.Errorf("event %d switched before every key was released", i)
		}
	}
	if last := results[5]; !last.Switch || last.Target != 2 {
		t.Errorf("last release = %+v, want a switch to machine 2", last)
	}
}

func TestOnlyTheDigitIsSwallowed(t *testing.T) {
	detector := newDetector(t, DefaultModifiers)
	results := feed(detector,
		press(keyLeftCtrl), press(keyLeftAlt), press(keyDigit2), repeat(keyDigit2),
		release(keyDigit2), release(keyLeftAlt), release(keyLeftCtrl),
	)

	want := []bool{false, false, true, true, true, false, false}
	for i, result := range results {
		if result.Swallow != want[i] {
			t.Errorf("event %d: Swallow = %v, want %v", i, result.Swallow, want[i])
		}
	}
}

func TestSequencesThatSwitch(t *testing.T) {
	cases := []struct {
		name   string
		events []event.Event
		want   int
	}{
		{
			name: "right-hand modifiers",
			events: []event.Event{
				press(keyRightCtrl), press(keyRightAlt), press(keyDigit1),
				release(keyDigit1), release(keyRightCtrl), release(keyRightAlt),
			},
			want: 1,
		},
		{
			name: "modifiers released before the digit",
			events: []event.Event{
				press(keyLeftCtrl), press(keyLeftAlt), press(keyDigit3),
				release(keyLeftCtrl), release(keyLeftAlt), release(keyDigit3),
			},
			want: 3,
		},
		{
			name: "a second digit replaces the first",
			events: []event.Event{
				press(keyLeftCtrl), press(keyLeftAlt), press(keyDigit2), release(keyDigit2),
				press(keyDigit3), release(keyDigit3), release(keyLeftAlt), release(keyLeftCtrl),
			},
			want: 3,
		},
		{
			name: "highest machine number",
			events: []event.Event{
				press(keyLeftCtrl), press(keyLeftAlt), press(keyDigit9),
				release(keyDigit9), release(keyLeftAlt), release(keyLeftCtrl),
			},
			want: 9,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := switches(feed(newDetector(t, DefaultModifiers), c.events...))
			if len(got) != 1 || got[0] != c.want {
				t.Errorf("switches = %v, want [%d]", got, c.want)
			}
		})
	}
}

func TestSequencesThatDoNotSwitch(t *testing.T) {
	cases := []struct {
		name   string
		events []event.Event
	}{
		{
			name:   "digit alone",
			events: []event.Event{press(keyDigit2), release(keyDigit2)},
		},
		{
			name: "only one of the two modifiers",
			events: []event.Event{
				press(keyLeftCtrl), press(keyDigit2), release(keyDigit2), release(keyLeftCtrl),
			},
		},
		{
			name: "an extra modifier is held",
			events: []event.Event{
				press(keyLeftCtrl), press(keyLeftAlt), press(keyLeftShift), press(keyDigit2),
				release(keyDigit2), release(keyLeftShift), release(keyLeftAlt), release(keyLeftCtrl),
			},
		},
		{
			name: "a mouse button is held, in the middle of a drag",
			events: []event.Event{
				press(event.ButtonLeft), press(keyLeftCtrl), press(keyLeftAlt), press(keyDigit2),
				release(keyDigit2), release(keyLeftAlt), release(keyLeftCtrl), release(event.ButtonLeft),
			},
		},
		{
			name: "another key is pressed after the digit",
			events: []event.Event{
				press(keyLeftCtrl), press(keyLeftAlt), press(keyDigit2), release(keyDigit2),
				press(event.KeyA), release(event.KeyA), release(keyLeftAlt), release(keyLeftCtrl),
			},
		},
		{
			name: "digit zero is not a machine",
			events: []event.Event{
				press(keyLeftCtrl), press(keyLeftAlt), press(keyDigit0),
				release(keyDigit0), release(keyLeftAlt), release(keyLeftCtrl),
			},
		},
		{
			name:   "modifiers alone",
			events: []event.Event{press(keyLeftCtrl), press(keyLeftAlt), release(keyLeftAlt), release(keyLeftCtrl)},
		},
		{
			name: "mouse movement and sync are ignored",
			events: []event.Event{
				event.Relative(event.RelX, 4), event.Sync(), event.Ping(),
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := switches(feed(newDetector(t, DefaultModifiers), c.events...)); len(got) != 0 {
				t.Errorf("switches = %v, want none", got)
			}
		})
	}
}

func TestDetectorWorksAgainAfterASwitch(t *testing.T) {
	detector := newDetector(t, DefaultModifiers)
	chord := func(digit uint16) []event.Event {
		return []event.Event{
			press(keyLeftCtrl), press(keyLeftAlt), press(digit),
			release(digit), release(keyLeftAlt), release(keyLeftCtrl),
		}
	}
	first := switches(feed(detector, chord(keyDigit2)...))
	second := switches(feed(detector, chord(keyDigit1)...))
	typed := feed(detector, press(keyDigit2), release(keyDigit2))

	if len(first) != 1 || first[0] != 2 || len(second) != 1 || second[0] != 1 {
		t.Errorf("switches = %v then %v, want [2] then [1]", first, second)
	}
	for _, result := range typed {
		if result.Swallow || result.Switch {
			t.Errorf("a plain digit after the hotkey gave %+v", result)
		}
	}
}

func TestOtherModifiersCanBeConfigured(t *testing.T) {
	detector := newDetector(t, " super + Shift ")
	got := switches(feed(detector,
		press(keyLeftSuper), press(keyRightShift), press(keyDigit2),
		release(keyDigit2), release(keyRightShift), release(keyLeftSuper),
	))
	if len(got) != 1 || got[0] != 2 {
		t.Errorf("switches = %v, want [2]", got)
	}
}

func TestInvalidModifiersAreRejected(t *testing.T) {
	for _, modifiers := range []string{"", "Ctrl+", "Ctrl+Hyper", "Ctrl+ctrl"} {
		if _, err := New(modifiers); err == nil {
			t.Errorf("New(%q) succeeded, want an error", modifiers)
		}
	}
}
