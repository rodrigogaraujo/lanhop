package macos

import (
	"slices"
	"sync"
	"testing"

	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/keymap"
)

var leftControlPressed = Observation{Type: TypeFlagsChanged, KeyCode: 0x3B, Flags: FlagControl}

func leftHeldWhileTheHardwareReadsItUp(buttonsDown map[int]bool) *translator {
	translator := newTranslator(keymap.Translator{}, fakeHardware{buttonsDown: buttonsDown})
	translated(translator, Observation{Type: TypeLeftMouseDown, Button: 0})
	return translator
}

func TestALeftButtonEndedByLanhopIsReleasedOnlyByTheRealUp(t *testing.T) {
	translator := leftHeldWhileTheHardwareReadsItUp(nil)

	translator.leftHeldUntilRealUp()

	expectTranslated(t, translated(translator, leftControlPressed), press(linuxLeftCtrl), event.Sync())
	expectTranslated(t, translated(translator, Observation{Type: TypeLeftMouseDragged, DX: 2, Flags: FlagControl}),
		event.Relative(event.RelX, 2), event.Sync())
	expectTranslated(t, translated(translator, Observation{Type: TypeLeftMouseUp, Button: 0, Flags: FlagControl}),
		release(event.ButtonLeft), event.Sync())
}

func TestOnlyTheLeftButtonIsKeptFromTheHardwareAfterLanhopEndedIt(t *testing.T) {
	translator := leftHeldWhileTheHardwareReadsItUp(nil)
	translated(translator, Observation{Type: TypeRightMouseDown, Button: 1})

	translator.leftHeldUntilRealUp()

	expectTranslated(t, translated(translator, leftControlPressed),
		press(linuxLeftCtrl), event.Sync(),
		release(event.ButtonRight), event.Sync(),
	)
}

func TestTheMarkOfTheLeftButtonEndsWithItsRelease(t *testing.T) {
	cases := map[string]Observation{
		"the real up":                 {Type: TypeLeftMouseUp, Button: 0},
		"a new press after a lost up": {Type: TypeLeftMouseDown, Button: 0},
		"input that the tap missed":   {Type: TypeTapDisabled},
	}
	for name, ending := range cases {
		t.Run(name, func(t *testing.T) {
			translator := leftHeldWhileTheHardwareReadsItUp(nil)
			translator.leftHeldUntilRealUp()

			translated(translator, ending)
			translated(translator, Observation{Type: TypeLeftMouseDown, Button: 0})

			got := translated(translator, leftControlPressed)
			if !slices.Contains(got, release(event.ButtonLeft)) {
				t.Errorf("after %s, a modifier pressed while the hardware reads the left button up gave %+v, want the left button released", name, got)
			}
		})
	}
}

func TestMarkingALeftButtonThatIsNotHeldChangesNothing(t *testing.T) {
	translator := newTranslator(keymap.Translator{}, fakeHardware{})
	translator.leftHeldUntilRealUp()
	translated(translator, Observation{Type: TypeLeftMouseDown, Button: 0})

	expectTranslated(t, translated(translator, leftControlPressed),
		press(linuxLeftCtrl), event.Sync(),
		release(event.ButtonLeft), event.Sync(),
	)
}

func TestTheLeftButtonCanBeMarkedWhileTheCaptureTranslates(t *testing.T) {
	translator := leftHeldWhileTheHardwareReadsItUp(map[int]bool{0: true})
	var marking sync.WaitGroup
	marking.Add(1)
	go func() {
		defer marking.Done()
		for range 100 {
			translator.leftHeldUntilRealUp()
		}
	}()
	for range 100 {
		translated(translator, Observation{Type: TypeLeftMouseDragged, DX: 1}, leftControlPressed, Observation{Type: TypeFlagsChanged, KeyCode: 0x3B})
	}
	marking.Wait()

	expectTranslated(t, translated(translator, Observation{Type: TypeLeftMouseUp, Button: 0}), release(event.ButtonLeft), event.Sync())
}
