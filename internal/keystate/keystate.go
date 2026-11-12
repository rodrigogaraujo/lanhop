package keystate

import (
	"slices"

	"github.com/rodrigogaraujo/lanhop/internal/event"
)

type Tracker struct {
	held map[uint16]struct{}
}

func (t *Tracker) Observe(e event.Event) {
	if e.Type != event.TypeKey {
		return
	}
	switch e.Value {
	case event.KeyPressed:
		if t.held == nil {
			t.held = make(map[uint16]struct{})
		}
		t.held[e.Code] = struct{}{}
	case event.KeyReleased:
		delete(t.held, e.Code)
	}
}

func (t *Tracker) Held() []uint16 {
	codes := make([]uint16, 0, len(t.held))
	for code := range t.held {
		codes = append(codes, code)
	}
	slices.Sort(codes)
	return codes
}

func (t *Tracker) ReleaseAll() []event.Event {
	codes := t.Held()
	if len(codes) == 0 {
		return nil
	}
	releases := make([]event.Event, 0, len(codes)+1)
	for _, code := range codes {
		releases = append(releases, event.Key(code, event.KeyReleased))
	}
	clear(t.held)
	return append(releases, event.Sync())
}
