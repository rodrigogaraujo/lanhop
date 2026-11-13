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
	Mark
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
	case Mark:
		return "mark"
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

type owed struct {
	due   int
	late  int
	stale int
}

type Desk[M comparable] struct {
	on           bool
	sides        map[M]*sideMachine
	connected    []M
	focused      M
	focusOnSide  bool
	last         string
	number       int
	hasCopy      bool
	centralHolds int
	answers      map[M]*owed
	readsDue     int
	staleReads   int
}

func NewDesk[M comparable](on bool) *Desk[M] {
	return &Desk[M]{on: on, sides: make(map[M]*sideMachine), answers: make(map[M]*owed)}
}

func (d *Desk[M]) Connected(side M) []Action[M] {
	if _, known := d.sides[side]; known {
		d.writeOffAnswersOf(side)
	} else {
		d.connected = append(d.connected, side)
	}
	d.sides[side] = &sideMachine{}
	return append([]Action[M]{{Do: Announce, To: side, On: d.on}}, d.deliverToFocused()...)
}

func (d *Desk[M]) Lost(side M) []Action[M] {
	d.writeOffAnswersOf(side)
	delete(d.sides, side)
	d.connected = slices.DeleteFunc(d.connected, func(m M) bool { return m == side })
	return d.deliverToFocused()
}

func (d *Desk[M]) GaveUpOn(side M) []Action[M] {
	if owing := d.answers[side]; owing != nil && owing.due > 0 {
		owing.due--
		owing.late++
		d.withdraw()
	}
	return d.deliverToFocused()
}

func (d *Desk[M]) Says(side M, takes bool) []Action[M] {
	machine, connected := d.sides[side]
	if !connected {
		return nil
	}
	tookPart := d.takingPart()
	machine.takes = takes
	if !takes {
		machine.holds = 0
		delete(d.answers, side)
	}
	var actions []Action[M]
	if !tookPart && d.takingPart() {
		actions = append(actions, Action[M]{Do: Mark})
	}
	return append(actions, d.deliverToFocused()...)
}

func (d *Desk[M]) Switch(on bool) []Action[M] {
	if on == d.on {
		return nil
	}
	d.on = on
	if !on {
		d.withdraw()
		d.staleReads += d.readsDue
		d.readsDue = 0
		for _, owing := range d.answers {
			owing.stale += owing.due + owing.late
			owing.due, owing.late = 0, 0
		}
	}
	actions := make([]Action[M], 0, len(d.connected)+1)
	for _, side := range d.connected {
		actions = append(actions, Action[M]{Do: Announce, To: side, On: on})
	}
	if d.takingPart() {
		actions = append(actions, Action[M]{Do: Mark})
	}
	return actions
}

func (d *Desk[M]) FocusOn(side M) []Action[M] {
	if d.focusOnSide && d.focused == side {
		return nil
	}
	var actions []Action[M]
	if !d.focusOnSide && d.takingPart() {
		d.readsDue++
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
	d.readArrived()
	return d.deliverToFocused()
}

func (d *Desk[M]) ReadGave(text string, readErr error) ([]Action[M], error) {
	if current := d.readArrived(); !current || !d.on {
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
	d.answerArrived(side)
	return d.deliverToFocused()
}

func (d *Desk[M]) WithdrawalFrom(side M) []Action[M] {
	return d.CopyFrom(side, "")
}

func (d *Desk[M]) CopyFrom(side M, text string) []Action[M] {
	current := d.answerArrived(side)
	machine, connected := d.sides[side]
	if !current || !d.on || !connected || !machine.takes {
		return d.deliverToFocused()
	}
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

func (d *Desk[M]) takingPart() bool {
	if !d.on {
		return false
	}
	for _, machine := range d.sides {
		if machine.takes {
			return true
		}
	}
	return false
}

func (d *Desk[M]) awaitTheSideThatLostFocus() {
	if !d.focusOnSide || !d.on {
		return
	}
	if machine, connected := d.sides[d.focused]; connected && machine.takes {
		owing := d.answers[d.focused]
		if owing == nil {
			owing = &owed{}
			d.answers[d.focused] = owing
		}
		owing.due++
	}
}

func (d *Desk[M]) answerArrived(side M) (current bool) {
	owing := d.answers[side]
	switch {
	case owing == nil:
		return true
	case owing.stale > 0:
		owing.stale--
		return false
	case owing.late > 0:
		owing.late--
	case owing.due > 0:
		owing.due--
	}
	return true
}

func (d *Desk[M]) writeOffAnswersOf(side M) {
	if owing := d.answers[side]; owing != nil && owing.due > 0 {
		d.withdraw()
	}
	delete(d.answers, side)
}

func (d *Desk[M]) readArrived() (current bool) {
	if d.staleReads > 0 {
		d.staleReads--
		return false
	}
	d.readsDue = max(d.readsDue-1, 0)
	return true
}

func (d *Desk[M]) answersStillDue() bool {
	for _, owing := range d.answers {
		if owing.due > 0 {
			return true
		}
	}
	return false
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
	if !d.on || !d.hasCopy || d.readsDue > 0 || d.answersStillDue() {
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

func (s *Side) CentralSays(takes bool) []Action[ToCentral] {
	tookPart := s.sharing()
	s.centralTakes = takes
	if !tookPart && s.sharing() {
		return []Action[ToCentral]{{Do: Mark}}
	}
	return nil
}

func (s *Side) Switch(on bool) []Action[ToCentral] {
	if on == s.on {
		return nil
	}
	s.on = on
	actions := []Action[ToCentral]{{Do: Announce, On: on}}
	if s.sharing() {
		actions = append(actions, Action[ToCentral]{Do: Mark})
	}
	return actions
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
	if !s.sharing() || !s.focused || text == "" {
		return nil
	}
	return []Action[ToCentral]{{Do: Write, Text: text}}
}

func (s *Side) sharing() bool {
	return s.on && s.centralTakes
}
