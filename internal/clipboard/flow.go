package clipboard

import "slices"

type Do int

const (
	Announce Do = iota + 1
	Send
	Withdraw
	Unchanged
	Write
	Read
)

func (d Do) String() string {
	switch d {
	case Announce:
		return "announce"
	case Send:
		return "send"
	case Withdraw:
		return "withdraw"
	case Unchanged:
		return "unchanged"
	case Write:
		return "write"
	case Read:
		return "read"
	}
	return "nothing"
}

type Action[M comparable] struct {
	Do   Do
	To   M
	Text string
	On   bool
}

type ToCentral struct{}

type sideMachine struct {
	takes bool
	holds int
}

type Desk[M comparable] struct {
	on                bool
	sides             map[M]*sideMachine
	connected         []M
	focused           M
	focusOnSide       bool
	last              string
	number            int
	hasCopy           bool
	centralHolds      int
	waitingFor        map[M]bool
	waitingForCentral bool
}

func NewDesk[M comparable](on bool) *Desk[M] {
	return &Desk[M]{on: on, sides: make(map[M]*sideMachine), waitingFor: make(map[M]bool)}
}

func (d *Desk[M]) Connected(side M) []Action[M] {
	if _, already := d.sides[side]; !already {
		d.connected = append(d.connected, side)
	}
	d.sides[side] = &sideMachine{}
	return []Action[M]{{Do: Announce, To: side, On: d.on}}
}

func (d *Desk[M]) Lost(side M) []Action[M] {
	delete(d.sides, side)
	d.connected = slices.DeleteFunc(d.connected, func(m M) bool { return m == side })
	return d.noAnswerFrom(side)
}

func (d *Desk[M]) GaveUpOn(side M) []Action[M] {
	return d.noAnswerFrom(side)
}

func (d *Desk[M]) noAnswerFrom(side M) []Action[M] {
	if d.waitingFor[side] {
		d.withdraw()
	}
	return d.answered(side)
}

func (d *Desk[M]) Says(side M, takes bool) []Action[M] {
	machine, connected := d.sides[side]
	if !connected {
		return nil
	}
	machine.takes = takes
	if !takes {
		machine.holds = 0
		delete(d.waitingFor, side)
	}
	return d.deliverToFocused()
}

func (d *Desk[M]) Switch(on bool) []Action[M] {
	if on == d.on {
		return nil
	}
	d.on = on
	if !on {
		d.withdraw()
		d.waitingForCentral = false
		clear(d.waitingFor)
	}
	actions := make([]Action[M], 0, len(d.connected))
	for _, side := range d.connected {
		actions = append(actions, Action[M]{Do: Announce, To: side, On: on})
	}
	return actions
}

func (d *Desk[M]) FocusOn(side M) []Action[M] {
	if d.focusOnSide && d.focused == side {
		return nil
	}
	var actions []Action[M]
	if !d.focusOnSide && d.on {
		d.waitingForCentral = true
		actions = append(actions, Action[M]{Do: Read})
	}
	d.awaitTheSideThatLostFocus()
	d.focused, d.focusOnSide = side, true
	return append(actions, d.deliverToFocused()...)
}

func (d *Desk[M]) FocusOnCentral() []Action[M] {
	if !d.focusOnSide {
		return nil
	}
	d.awaitTheSideThatLostFocus()
	var nobody M
	d.focused, d.focusOnSide = nobody, false
	return d.deliverToFocused()
}

func (d *Desk[M]) ReadUnchanged() []Action[M] {
	d.waitingForCentral = false
	return d.deliverToFocused()
}

func (d *Desk[M]) ReadGave(text string, readErr error) ([]Action[M], error) {
	d.waitingForCentral = false
	if !d.on {
		return nil, nil
	}
	if readErr == nil {
		readErr = sendable(text)
	}
	if readErr != nil {
		d.withdraw()
		return d.deliverToFocused(), readErr
	}
	if !d.hasCopy || text != d.last {
		d.newCopy(text)
	}
	d.centralHolds = d.number
	return d.deliverToFocused(), nil
}

func (d *Desk[M]) SendStopped(side M) {
	if machine, connected := d.sides[side]; connected {
		machine.holds = 0
	}
}

func (d *Desk[M]) UnchangedOn(side M) []Action[M] {
	return d.answered(side)
}

func (d *Desk[M]) CopyFrom(side M, text string) []Action[M] {
	machine, connected := d.sides[side]
	if !d.on || !connected || !machine.takes {
		return d.answered(side)
	}
	delete(d.waitingFor, side)
	switch {
	case text == "":
		d.withdraw()
	case !d.hasCopy || text != d.last:
		d.newCopy(text)
		machine.holds = d.number
	default:
		machine.holds = d.number
	}
	return d.deliverToFocused()
}

func (d *Desk[M]) awaitTheSideThatLostFocus() {
	if !d.focusOnSide || !d.on {
		return
	}
	if machine, connected := d.sides[d.focused]; connected && machine.takes {
		d.waitingFor[d.focused] = true
	}
}

func (d *Desk[M]) answered(side M) []Action[M] {
	delete(d.waitingFor, side)
	return d.deliverToFocused()
}

func (d *Desk[M]) newCopy(text string) {
	d.number++
	d.last = text
	d.hasCopy = true
}

func (d *Desk[M]) withdraw() {
	d.last = ""
	d.hasCopy = false
}

func (d *Desk[M]) deliverToFocused() []Action[M] {
	if !d.on || !d.hasCopy || d.waitingForCentral || len(d.waitingFor) > 0 {
		return nil
	}
	if !d.focusOnSide {
		if d.centralHolds == d.number {
			return nil
		}
		d.centralHolds = d.number
		return []Action[M]{{Do: Write, Text: d.last}}
	}
	machine, connected := d.sides[d.focused]
	if !connected || !machine.takes || machine.holds == d.number {
		return nil
	}
	machine.holds = d.number
	return []Action[M]{{Do: Send, To: d.focused, Text: d.last}}
}

type Side struct {
	on           bool
	centralTakes bool
	focused      bool
}

func NewSide(on bool) *Side {
	return &Side{on: on}
}

func (s *Side) Connected() []Action[ToCentral] {
	s.centralTakes = false
	s.focused = false
	return []Action[ToCentral]{{Do: Announce, On: s.on}}
}

func (s *Side) CentralSays(takes bool) {
	s.centralTakes = takes
}

func (s *Side) Switch(on bool) []Action[ToCentral] {
	if on == s.on {
		return nil
	}
	s.on = on
	return []Action[ToCentral]{{Do: Announce, On: on}}
}

func (s *Side) FocusArrives() {
	s.focused = true
}

func (s *Side) FocusLeaves() []Action[ToCentral] {
	s.focused = false
	if !s.sharing() {
		return nil
	}
	return []Action[ToCentral]{{Do: Read}}
}

func (s *Side) ReadUnchanged() []Action[ToCentral] {
	if !s.sharing() {
		return nil
	}
	return []Action[ToCentral]{{Do: Unchanged}}
}

func (s *Side) ReadGave(text string, readErr error) ([]Action[ToCentral], error) {
	if !s.sharing() {
		return nil, nil
	}
	if readErr == nil {
		readErr = sendable(text)
	}
	if readErr != nil {
		return []Action[ToCentral]{{Do: Withdraw}}, readErr
	}
	return []Action[ToCentral]{{Do: Send, Text: text}}, nil
}

func (s *Side) CopyFromCentral(text string) []Action[ToCentral] {
	if !s.on || !s.focused || text == "" {
		return nil
	}
	return []Action[ToCentral]{{Do: Write, Text: text}}
}

func (s *Side) sharing() bool {
	return s.on && s.centralTakes
}
