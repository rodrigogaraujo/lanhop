package features

import (
	"sync"
	"testing"

	"github.com/rodrigogaraujo/lanhop/internal/event"
)

func TestTurningOneBitLeavesTheOthersAsTheyWere(t *testing.T) {
	var set Set
	set.Turn(8, true)

	if got := set.Turn(Clipboard, true); got != 1|1<<8 {
		t.Errorf("after turning the clipboard on, the value is %#x, want %#x", got, 1|1<<8)
	}
	if got := set.Turn(Clipboard, false); got != 1<<8 {
		t.Errorf("after turning the clipboard off, the value is %#x, want %#x", got, 1<<8)
	}
	if !set.Has(8) || set.Has(Clipboard) {
		t.Errorf("Has(8) = %v and Has(Clipboard) = %v, want true and false", set.Has(8), set.Has(Clipboard))
	}
}

func TestTheClipboardBitIsTheOneOnTheWire(t *testing.T) {
	var set Set

	if got := set.Turn(Clipboard, true); got != event.FeatureClipboard {
		t.Errorf("the clipboard bit is %#x, want the wire value %#x", got, event.FeatureClipboard)
	}
}

func TestBitsTurnedAtOnceFromSeveralGoroutinesAreAllKept(t *testing.T) {
	var set Set
	var turning sync.WaitGroup
	for bit := range 16 {
		turning.Add(1)
		go func() {
			defer turning.Done()
			for range 1000 {
				set.Turn(bit, false)
				set.Turn(bit, true)
			}
		}()
	}
	turning.Wait()

	if got := set.Value(); got != 1<<16-1 {
		t.Errorf("after every goroutine left its bit on, the value is %#x, want %#x", got, 1<<16-1)
	}
}
