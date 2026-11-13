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
}

type owed struct {
	due   int
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
	readsDue       int
	staleReads     int
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
	if owing := d.answers[side]; owing != nil && owing.due > 0 {
		owing.due--
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
	if owing := d.answers[side]; owing != nil && d.shares(machine) == 0 {
		owing.due, owing.late = 0, 0
	}
	if takes == 0 {
		delete(d.answers, side)
	}
	if !takes.Has(Files) {
		d.forgetFilesCopiedOn(side)
	}
	var actions []Action[M]
	if d.sharedKinds()&^sharedBefore != 0 {
		actions = append(actions, Action[M]{Do: Mark})
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
		d.staleReads += d.readsDue
		d.readsDue = 0
	}
	for side, owing := range d.answers {
		if machine, connected := d.sides[side]; !connected || d.shares(machine) == 0 {
			owing.stale += owing.due + owing.late
			owing.due, owing.late = 0, 0
		}
	}
	actions := make([]Action[M], 0, len(d.connected)+1)
	for _, side := range d.connected {
		actions = append(actions, Action[M]{Do: Announce, To: side, Takes: on})
	}
	if d.sharedKinds()&^sharedBefore != 0 {
		actions = append(actions, Action[M]{Do: Mark})
	}
	return append(actions, d.deliverToFocused()...)
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
	d.endedThisVisit = 0
	return append(actions, d.deliverToFocused()...)
}

func (d *Desk[M]) FocusOnCentral() []Action[M] {
	if !d.focusOnSide {
		return nil
	}
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
		return []Action[M]{{Do: WriteFiles, Transfer: transfer, FileCopy: d.last.files}}
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
	if current := d.readArrived(); !current || d.on == 0 {
		return nil, nil
	}
	if readErr == nil && !d.on.Has(read.kind()) {
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
	current := d.answerArrived(side)
	machine, connected := d.sides[side]
	if !current || !connected || d.shares(machine) == 0 {
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
	if !d.focusOnSide || d.on == 0 {
		return
	}
	if machine, connected := d.sides[d.focused]; connected && d.shares(machine) != 0 {
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

func (d *Desk[M]) forgetFilesCopiedOn(side M) {
	if d.hasCopy && d.last.filesOnASide && d.last.filesOn == side {
		d.withdraw()
	}
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
	if d.on == 0 || !d.hasCopy || d.readsDue > 0 || d.answersStillDue() {
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
	if !connected || !d.shares(machine).Has(kind) || machine.holds == d.number {
		return actions
	}
	if kind == Files {
		return append(actions, d.startTransfer()...)
	}
	machine.holds = d.number
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
		actions = append(actions, Action[M]{Do: Fetch, To: d.last.filesOn, Transfer: d.transfers, FileCopy: d.last.files})
	}
	if d.focusOnSide {
		actions = append(actions, Action[M]{Do: Deliver, To: d.focused, Transfer: d.transfers, FileCopy: d.last.files})
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
		actions = append(actions, Action[ToCentral]{Do: Read})
	}
	return actions
}

func (s *Side) ReadUnchanged() []Action[ToCentral] {
	if s.sharing() == 0 {
		return nil
	}
	return []Action[ToCentral]{{Do: Unchanged}}
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
		return nil, nil
	}
	if readErr == nil && !s.sharing().Has(kind) {
		readErr = notShared(kind)
	}
	s.offersFiles = readErr == nil && kind == Files
	if readErr != nil {
		return []Action[ToCentral]{{Do: Withdraw}}, readErr
	}
	return []Action[ToCentral]{answer}, nil
}

func (s *Side) sharingChanged(sharedBefore Kinds) []Action[ToCentral] {
	var actions []Action[ToCentral]
	if !s.sharing().Has(Files) {
		s.offersFiles = false
		actions = s.stopTransfers()
	}
	if s.sharing()&^sharedBefore != 0 {
		actions = append(actions, Action[ToCentral]{Do: Mark})
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
