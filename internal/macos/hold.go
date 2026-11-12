package macos

import "sync"

type holdingScreen interface {
	cursorScreen
	PutCursorBack(at Point)
	HideCursor()
	ShowCursor()
}

type hold struct {
	holdingScreen
	accepts func(Observation) bool
	hotkey  *hotkeyGuard
	state   sync.Mutex
	taken   bool
	hidden  bool
	cursor  Point
}

func (h *hold) take() {
	cursor := h.Cursor()
	h.state.Lock()
	defer h.state.Unlock()
	h.taken, h.cursor = true, cursor
	if !h.hidden {
		h.hidden = true
		h.HideCursor()
	}
}

func (h *hold) letGo() {
	h.state.Lock()
	defer h.state.Unlock()
	h.taken = false
	if h.hidden {
		h.hidden = false
		h.ShowCursor()
	}
}

func (h *hold) PlaceCursor(at Point) {
	h.state.Lock()
	h.cursor = at
	h.state.Unlock()
	h.holdingScreen.PlaceCursor(at)
}

func (h *hold) keeps(seen Observation) bool {
	h.state.Lock()
	defer h.state.Unlock()
	if !h.accepts(seen) {
		return false
	}
	digitOfTheHotkey := h.hotkey.keeps(seen)
	if !h.taken {
		return digitOfTheHotkey
	}
	if movesTheCursor(seen.Type) {
		h.PutCursorBack(h.cursor)
	}
	return true
}

func movesTheCursor(observed uint32) bool {
	switch observed {
	case TypeMouseMoved, TypeLeftMouseDragged, TypeRightMouseDragged, TypeOtherMouseDragged:
		return true
	}
	return false
}
