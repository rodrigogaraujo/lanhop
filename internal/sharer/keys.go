package sharer

import "github.com/rodrigogaraujo/lanhop/internal/event"

type keyHooks struct {
	escape         func(duringADrag bool)
	escapeHeldBack bool
	leftButton     func(pressed bool)
}

func (s *Sharer) OnEscape(pressed func(duringADrag bool)) {
	s.keys.escape = pressed
}

func (s *Sharer) OnLeftButton(changed func(pressed bool)) {
	s.keys.leftButton = changed
}

func (s *Sharer) tellLeftButton(captured event.Event) {
	if captured.Type != event.TypeKey || captured.Code != event.ButtonLeft || captured.Value == event.KeyRepeated {
		return
	}
	if s.keys.leftButton != nil {
		s.keys.leftButton(captured.Value == event.KeyPressed)
	}
}

func (s *Sharer) escapeStaysHere(captured event.Event) bool {
	if captured.Type != event.TypeKey || captured.Code != event.KeyEsc {
		return false
	}
	if captured.Value != event.KeyPressed {
		return s.keys.escapeHeldBack
	}
	drag := s.dragHere()
	s.keys.escapeHeldBack = drag != nil && s.focused != nil && s.focused != drag.origin
	if s.keys.escape != nil {
		s.keys.escape(drag != nil)
	}
	return s.keys.escapeHeldBack
}
