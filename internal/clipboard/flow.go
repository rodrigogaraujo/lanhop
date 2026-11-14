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
	Describe
	Fetch
	Deliver
	SendFiles
	ReceiveFiles
	WriteFiles
	Stop
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
	case Describe:
		return "describe"
	case Fetch:
		return "fetch"
	case Deliver:
		return "deliver"
	case SendFiles:
		return "send files"
	case ReceiveFiles:
		return "receive files"
	case WriteFiles:
		return "write files"
	case Stop:
		return "stop"
	}
	return "nothing"
}

type Action[M comparable] struct {
	Do       Do
	To       M
	Text     string
	FileCopy FileCopy
	Transfer int
	Takes    Kinds
}

type ToCentral struct{}

type sideMachine struct {
	takes Kinds
	holds int
	sent  int
}

type owed struct {
	due   []int
	late  int
	stale int
}

type deskCopy[M comparable] struct {
	text         string
	files        FileCopy
	filesOn      M
	filesOnASide bool
}

func (c deskCopy[M]) kind() Kinds {
	if c.files.FilesAndFolders > 0 {
		return Files
	}
	return Text
}

func (c deskCopy[M]) empty() bool {
	return c.text == "" && c.files.FilesAndFolders == 0
}

func (c deskCopy[M]) sameTextAs(other deskCopy[M]) bool {
	return c.kind() == Text && other.kind() == Text && c.text == other.text
}

type transfer[M comparable] struct {
	id      int
	number  int
	to      M
	toASide bool
}

type Desk[M comparable] struct {
	on             Kinds
	sides          map[M]*sideMachine
	connected      []M
	focused        M
	focusOnSide    bool
	last           deskCopy[M]
	number         int
	hasCopy        bool
	centralHolds   int
	answers        map[M]*owed
	readsDue       []int
	staleReads     int
	leaves         int
	lastLeave      int
	moving         *transfer[M]
	transfers      int
	endedThisVisit int
}

func NewDesk[M comparable](on Kinds) *Desk[M] {
	return &Desk[M]{on: on, sides: make(map[M]*sideMachine), answers: make(map[M]*owed)}
}

func (d *Desk[M]) Connected(side M) []Action[M] {
	if _, known := d.sides[side]; known {
		d.writeOffAnswersOf(side)
		d.forgetFilesCopiedOn(side)
	} else {
		d.connected = append(d.connected, side)
	}
	d.sides[side] = &sideMachine{}
	return append([]Action[M]{{Do: Announce, To: side, Takes: d.on}}, d.deliverToFocused()...)
}

func (d *Desk[M]) Lost(side M) []Action[M] {
	d.writeOffAnswersOf(side)
	d.forgetFilesCopiedOn(side)
	delete(d.sides, side)
	d.connected = slices.DeleteFunc(d.connected, func(m M) bool { return m == side })
	return d.deliverToFocused()
}

func (d *Desk[M]) GaveUpOn(side M) []Action[M] {
	if owing := d.answers[side]; owing != nil && len(owing.due) > 0 {
		d.lastLeave = max(d.lastLeave, owing.due[0])
		owing.due = owing.due[1:]
		owing.late++
		d.withdraw()
	}
	return d.deliverToFocused()
}

func (d *Desk[M]) Says(side M, takes Kinds) []Action[M] {
	machine, connected := d.sides[side]
	if !connected {
		return nil
	}
	sharedBefore := d.sharedKinds()
	machine.takes = takes
	if d.hasCopy && !takes.Has(d.last.kind()) {
		machine.holds = 0
	}
	if d.shares(machine) == 0 {
		d.stopWaitingFor(side)
	}
	if !takes.Has(Files) {
		d.forgetFilesCopiedOn(side)
	}
	var actions []Action[M]
	if started := d.sharedKinds() &^ sharedBefore; started != 0 {
		actions = append(actions, Action[M]{Do: Mark, Takes: started})
	}
	return append(actions, d.deliverToFocused()...)
}

func (d *Desk[M]) Switch(on Kinds) []Action[M] {
	if on == d.on {
		return nil
	}
	sharedBefore := d.sharedKinds()
	d.on = on
	if d.hasCopy && !on.Has(d.last.kind()) {
		d.withdraw()
	}
	if on == 0 {
		d.staleReads += len(d.readsDue)
		d.readsDue = nil
	}
	for side, machine := range d.sides {
		if d.shares(machine) == 0 {
			d.stopWaitingFor(side)
		}
	}
	actions := make([]Action[M], 0, len(d.connected)+1)
	for _, side := range d.connected {
		actions = append(actions, Action[M]{Do: Announce, To: side, Takes: on})
	}
	if started := d.sharedKinds() &^ sharedBefore; started != 0 {
		actions = append(actions, Action[M]{Do: Mark, Takes: started})
	}
	return append(actions, d.deliverToFocused()...)
}

func (d *Desk[M]) FocusOn(side M) []Action[M] {
	if d.focusOnSide && d.focused == side {
		return nil
	}
	d.leaves++
	var actions []Action[M]
	if !d.focusOnSide && d.takingPart() {
		d.readsDue = append(d.readsDue, d.leaves)
		actions = append(actions, Action[M]{Do: Read})
	}
	d.awaitTheSideThatLostFocus()
	d.focused, d.focusOnSide = side, true
	d.endedThisVisit = 0
	return append(actions, d.deliverToFocused()...)
}

func (d *Desk[M]) FocusOnCentral() []Action[M] {
	if !d.focusOnSide {
		return nil
	}
	d.leaves++
	d.awaitTheSideThatLostFocus()
	var nobody M
	d.focused, d.focusOnSide = nobody, false
	d.endedThisVisit = 0
	return d.deliverToFocused()
}

func (d *Desk[M]) ReadUnchanged() []Action[M] {
	d.readArrived()
	return d.deliverToFocused()
}

func (d *Desk[M]) ReadGave(text string, readErr error) ([]Action[M], error) {
	if readErr == nil {
		readErr = sendable(text)
	}
	return d.readGave(deskCopy[M]{text: text}, readErr)
}

func (d *Desk[M]) ReadGaveFiles(files FileCopy, readErr error) ([]Action[M], error) {
	if readErr == nil {
		readErr = sendableFiles(files)
	}
	return d.readGave(deskCopy[M]{files: files}, readErr)
}

func (d *Desk[M]) SendStopped(side M) {
	if machine, connected := d.sides[side]; connected && machine.holds == machine.sent {
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
	actions, _ := d.answer(side, deskCopy[M]{text: text}, nil)
	return actions
}

func (d *Desk[M]) FileCopyFrom(side M, files FileCopy) ([]Action[M], error) {
	return d.answer(side, deskCopy[M]{files: files, filesOn: side, filesOnASide: true}, sendableFiles(files))
}

func (d *Desk[M]) FilesArrived(transfer int) []Action[M] {
	moving := d.moving
	if moving == nil || moving.id != transfer {
		return nil
	}
	d.moving = nil
	if !moving.toASide {
		d.centralHolds = moving.number
		return []Action[M]{{Do: WriteFiles, Transfer: transfer}}
	}
	if machine, connected := d.sides[moving.to]; connected {
		machine.holds = moving.number
	}
	return nil
}

func (d *Desk[M]) TransferEnded(transfer int) {
	if d.moving == nil || d.moving.id != transfer {
		return
	}
	d.endedThisVisit = d.moving.number
	d.moving = nil
}

func (d *Desk[M]) readGave(read deskCopy[M], readErr error) ([]Action[M], error) {
	current, leave := d.readArrived()
	if !current || d.on == 0 {
		return nil, nil
	}
	if !d.takesTheResultOf(leave) {
		return d.deliverToFocused(), nil
	}
	if readErr == nil && !d.sharedKinds().Has(read.kind()) {
		readErr = notShared(read.kind())
	}
	if readErr != nil {
		d.withdraw()
		return d.deliverToFocused(), readErr
	}
	if !d.hasCopy || !d.last.sameTextAs(read) {
		d.newCopy(read)
	}
	d.centralHolds = d.number
	return d.deliverToFocused(), nil
}

func (d *Desk[M]) answer(side M, answer deskCopy[M], refusal error) ([]Action[M], error) {
	current, leave := d.answerArrived(side)
	machine, connected := d.sides[side]
	if !current || !connected || d.shares(machine) == 0 || !d.takesTheResultOf(leave) {
		return d.deliverToFocused(), nil
	}
	if refusal == nil && !d.shares(machine).Has(answer.kind()) {
		refusal = notShared(answer.kind())
	}
	switch {
	case refusal != nil || answer.empty():
		d.withdraw()
	case !d.hasCopy || !d.last.sameTextAs(answer):
		d.newCopy(answer)
		machine.holds = d.number
	default:
		machine.holds = d.number
	}
	return d.deliverToFocused(), refusal
}

func (d *Desk[M]) shares(machine *sideMachine) Kinds {
	return machine.takes & d.on
}

func (d *Desk[M]) sharedKinds() Kinds {
	var shared Kinds
	for _, machine := range d.sides {
		shared |= d.shares(machine)
	}
	return shared
}

func (d *Desk[M]) takingPart() bool {
	return d.sharedKinds() != 0
}

func (d *Desk[M]) awaitTheSideThatLostFocus() {
	if !d.focusOnSide {
		return
	}
	machine, connected := d.sides[d.focused]
	if !connected {
		return
	}
	owing := d.answers[d.focused]
	if owing == nil {
		owing = &owed{}
		d.answers[d.focused] = owing
	}
	if d.shares(machine) == 0 {
		owing.stale++
	} else {
		owing.due = append(owing.due, d.leaves)
	}
}

func (d *Desk[M]) stopWaitingFor(side M) {
	if owing := d.answers[side]; owing != nil {
		owing.stale += len(owing.due) + owing.late
		owing.due, owing.late = nil, 0
	}
}

func (d *Desk[M]) answerArrived(side M) (current bool, leave int) {
	owing := d.answers[side]
	switch {
	case owing == nil:
		return true, d.lastLeave
	case owing.stale > 0:
		owing.stale--
		return false, 0
	case owing.late > 0:
		owing.late--
		return true, d.lastLeave
	case len(owing.due) > 0:
		leave, owing.due = owing.due[0], owing.due[1:]
		return true, leave
	}
	return true, d.lastLeave
}

func (d *Desk[M]) writeOffAnswersOf(side M) {
	if owing := d.answers[side]; owing != nil && len(owing.due) > 0 {
		d.withdraw()
	}
	delete(d.answers, side)
}

func (d *Desk[M]) forgetFilesCopiedOn(side M) {
	if d.filesAreOn(side) {
		d.withdraw()
	}
}

func (d *Desk[M]) filesAreOn(side M) bool {
	return d.hasCopy && d.last.filesOnASide && d.last.filesOn == side
}

func (d *Desk[M]) readArrived() (current bool, leave int) {
	if d.staleReads > 0 {
		d.staleReads--
		return false, 0
	}
	if len(d.readsDue) == 0 {
		return true, d.lastLeave
	}
	leave, d.readsDue = d.readsDue[0], d.readsDue[1:]
	return true, leave
}

func (d *Desk[M]) takesTheResultOf(leave int) bool {
	if leave < d.lastLeave {
		return false
	}
	d.lastLeave = leave
	return true
}

func (d *Desk[M]) answersStillDue() bool {
	for _, owing := range d.answers {
		if len(owing.due) > 0 {
			return true
		}
	}
	return false
}

func (d *Desk[M]) newCopy(c deskCopy[M]) {
	d.number++
	d.last = c
	d.hasCopy = true
}

func (d *Desk[M]) withdraw() {
	d.last = deskCopy[M]{}
	d.hasCopy = false
}

func (d *Desk[M]) deliverToFocused() []Action[M] {
	actions := d.stopTransferNoLongerWanted()
	if d.on == 0 || !d.hasCopy || len(d.readsDue) > 0 || d.answersStillDue() {
		return actions
	}
	kind := d.last.kind()
	if !d.focusOnSide {
		if d.centralHolds == d.number {
			return actions
		}
		if kind == Files {
			return append(actions, d.startTransfer()...)
		}
		d.centralHolds = d.number
		return append(actions, Action[M]{Do: Write, Text: d.last.text})
	}
	machine, connected := d.sides[d.focused]
	if !connected || !d.shares(machine).Has(kind) || machine.holds == d.number || d.filesAreOn(d.focused) {
		return actions
	}
	if kind == Files {
		return append(actions, d.startTransfer()...)
	}
	machine.holds, machine.sent = d.number, d.number
	return append(actions, Action[M]{Do: Send, To: d.focused, Text: d.last.text})
}

func (d *Desk[M]) startTransfer() []Action[M] {
	if d.moving != nil || d.endedThisVisit == d.number {
		return nil
	}
	d.transfers++
	d.moving = &transfer[M]{id: d.transfers, number: d.number, to: d.focused, toASide: d.focusOnSide}
	var actions []Action[M]
	if d.last.filesOnASide {
		actions = append(actions, Action[M]{Do: Fetch, To: d.last.filesOn, Transfer: d.transfers})
	}
	if d.focusOnSide {
		actions = append(actions, Action[M]{Do: Deliver, To: d.focused, Transfer: d.transfers})
	}
	return actions
}

func (d *Desk[M]) stopTransferNoLongerWanted() []Action[M] {
	if d.moving == nil || d.stillWanted(*d.moving) {
		return nil
	}
	stopped := d.moving.id
	d.moving = nil
	return []Action[M]{{Do: Stop, Transfer: stopped}}
}

func (d *Desk[M]) stillWanted(moving transfer[M]) bool {
	if !d.hasCopy || moving.number != d.number || moving.toASide != d.focusOnSide {
		return false
	}
	if !moving.toASide {
		return true
	}
	machine, connected := d.sides[moving.to]
	return moving.to == d.focused && connected && d.shares(machine).Has(Files)
}

func notShared(kind Kinds) error {
	if kind == Files {
		return ErrFilesNotShared
	}
	return ErrTextNotShared
}

type Side struct {
	on           Kinds
	centralTakes Kinds
	focused      bool
	offersFiles  bool
	receiving    int
	isReceiving  bool
	sending      []int

	unchangedQueuedBehindReads []int
}

func NewSide(on Kinds) *Side {
	return &Side{on: on}
}

func (s *Side) Connected() []Action[ToCentral] {
	*s = Side{on: s.on}
	return []Action[ToCentral]{{Do: Announce, Takes: s.on}}
}

func (s *Side) CentralSays(takes Kinds) []Action[ToCentral] {
	sharedBefore := s.sharing()
	s.centralTakes = takes
	return s.sharingChanged(sharedBefore)
}

func (s *Side) Switch(on Kinds) []Action[ToCentral] {
	if on == s.on {
		return nil
	}
	sharedBefore := s.sharing()
	s.on = on
	return append([]Action[ToCentral]{{Do: Announce, Takes: on}}, s.sharingChanged(sharedBefore)...)
}

func (s *Side) FocusArrives() {
	s.focused = true
}

func (s *Side) FocusLeaves() []Action[ToCentral] {
	s.focused = false
	actions := s.stopReceiving()
	if s.sharing() != 0 {
		s.unchangedQueuedBehindReads = append(s.unchangedQueuedBehindReads, 0)
		return append(actions, Action[ToCentral]{Do: Read})
	}
	if running := len(s.unchangedQueuedBehindReads); running > 0 {
		s.unchangedQueuedBehindReads[running-1]++
		return actions
	}
	return append(actions, Action[ToCentral]{Do: Unchanged})
}

func (s *Side) ReadUnchanged() []Action[ToCentral] {
	return s.readEnded([]Action[ToCentral]{{Do: Unchanged}})
}

func (s *Side) ReadGave(text string, readErr error) ([]Action[ToCentral], error) {
	if readErr == nil {
		readErr = sendable(text)
	}
	return s.answer(Text, Action[ToCentral]{Do: Send, Text: text}, readErr)
}

func (s *Side) ReadGaveFiles(files FileCopy, readErr error) ([]Action[ToCentral], error) {
	if readErr == nil {
		readErr = sendableFiles(files)
	}
	return s.answer(Files, Action[ToCentral]{Do: Describe, FileCopy: files}, readErr)
}

func (s *Side) CopyFromCentral(text string) []Action[ToCentral] {
	if !s.sharing().Has(Text) || !s.focused || text == "" {
		return nil
	}
	return []Action[ToCentral]{{Do: Write, Text: text}}
}

func (s *Side) CentralWantsFiles(transfer int) []Action[ToCentral] {
	if !s.offersFiles {
		return []Action[ToCentral]{{Do: Stop, Transfer: transfer}}
	}
	s.sending = append(s.sending, transfer)
	return []Action[ToCentral]{{Do: SendFiles, Transfer: transfer}}
}

func (s *Side) CentralSendsFiles(transfer int) []Action[ToCentral] {
	if !s.focused || !s.sharing().Has(Files) {
		return []Action[ToCentral]{{Do: Stop, Transfer: transfer}}
	}
	actions := s.stopReceiving()
	s.receiving, s.isReceiving = transfer, true
	return append(actions, Action[ToCentral]{Do: ReceiveFiles, Transfer: transfer})
}

func (s *Side) FilesArrived(transfer int) []Action[ToCentral] {
	if !s.isReceiving || s.receiving != transfer {
		return nil
	}
	s.isReceiving = false
	return []Action[ToCentral]{{Do: WriteFiles, Transfer: transfer}}
}

func (s *Side) TransferEnded(transfer int) {
	if s.isReceiving && s.receiving == transfer {
		s.isReceiving = false
	}
	s.sending = slices.DeleteFunc(s.sending, func(sent int) bool { return sent == transfer })
}

func (s *Side) answer(kind Kinds, answer Action[ToCentral], readErr error) ([]Action[ToCentral], error) {
	if s.sharing() == 0 {
		return s.readEnded([]Action[ToCentral]{{Do: Unchanged}}), nil
	}
	if readErr == nil && !s.sharing().Has(kind) {
		readErr = notShared(kind)
	}
	s.offersFiles = readErr == nil && kind == Files
	if readErr != nil {
		return s.readEnded([]Action[ToCentral]{{Do: Withdraw}}), readErr
	}
	return s.readEnded([]Action[ToCentral]{answer}), nil
}

func (s *Side) readEnded(answer []Action[ToCentral]) []Action[ToCentral] {
	if len(s.unchangedQueuedBehindReads) == 0 {
		return answer
	}
	queued := s.unchangedQueuedBehindReads[0]
	s.unchangedQueuedBehindReads = s.unchangedQueuedBehindReads[1:]
	for range queued {
		answer = append(answer, Action[ToCentral]{Do: Unchanged})
	}
	return answer
}

func (s *Side) sharingChanged(sharedBefore Kinds) []Action[ToCentral] {
	var actions []Action[ToCentral]
	if !s.sharing().Has(Files) {
		s.offersFiles = false
		actions = s.stopTransfers()
	}
	if started := s.sharing() &^ sharedBefore; started != 0 {
		actions = append(actions, Action[ToCentral]{Do: Mark, Takes: started})
	}
	return actions
}

func (s *Side) stopReceiving() []Action[ToCentral] {
	if !s.isReceiving {
		return nil
	}
	s.isReceiving = false
	return []Action[ToCentral]{{Do: Stop, Transfer: s.receiving}}
}

func (s *Side) stopTransfers() []Action[ToCentral] {
	actions := s.stopReceiving()
	for _, sending := range s.sending {
		actions = append(actions, Action[ToCentral]{Do: Stop, Transfer: sending})
	}
	s.sending = nil
	return actions
}

func (s *Side) sharing() Kinds {
	return s.on & s.centralTakes
}
