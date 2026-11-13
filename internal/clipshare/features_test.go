package clipshare

import (
	"testing"

	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/features"
)

func TestAnAnnouncementCarriesEveryFeatureOfThisProcess(t *testing.T) {
	shared := new(features.Set)
	shared.Turn(8, true)
	config := Config{Features: shared}.withDefaults()

	if got := config.announcement(true).Value; got != event.FeatureClipboard|1<<8 {
		t.Errorf("announcing the clipboard on sent %#x, want %#x", got, event.FeatureClipboard|1<<8)
	}
	if got := config.announcement(false).Value; got != 1<<8 {
		t.Errorf("announcing the clipboard off sent %#x, want %#x", got, 1<<8)
	}
}

func TestOnlyTheClipboardBitSaysWhetherTheOtherEndTakesCopies(t *testing.T) {
	if takesCopies(event.Features(1 << 8)) {
		t.Error("a machine that announced only another feature counts as taking copies")
	}
	if !takesCopies(event.Features(event.FeatureClipboard | 1<<8)) {
		t.Error("a machine that announced the clipboard and another feature does not count as taking copies")
	}
}
