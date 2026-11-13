package transport

import (
	"sync"
	"testing"

	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/features"
)

func TestTheLastFeaturesWrittenCarriesEveryBitTurnedBeforeIt(t *testing.T) {
	sharerSide, receiverSide := pairedMachines(t)
	announced := new(features.Set)
	const senders = 8

	var sending sync.WaitGroup
	for bit := range senders {
		sending.Add(1)
		go func() {
			defer sending.Done()
			announced.Turn(bit, true)
			if err := receiverSide.SendFeatures(announced); err != nil {
				t.Errorf("SendFeatures: %v", err)
			}
		}()
	}
	sending.Wait()

	var last event.Event
	for range senders {
		last = receive(t, sharerSide)
		if last.Code != event.ControlFeatures {
			t.Fatalf("got %+v, want only Features frames", last)
		}
	}
	if want := int32(1<<senders - 1); last.Value != want {
		t.Errorf("the last Features frame carried %#x, want every bit turned before it, %#x", last.Value, want)
	}
}
