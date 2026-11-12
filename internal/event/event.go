package event

type Event struct {
	Type  uint16
	Code  uint16
	Value int32
}

const (
	TypeSync     uint16 = 0x00
	TypeKey      uint16 = 0x01
	TypeRelative uint16 = 0x02
	TypeControl  uint16 = 0xFF00
)

const ProtocolVersion int32 = 1

const (
	ControlHello uint16 = 1
	ControlPing  uint16 = 2
	ControlEnter uint16 = 3
	ControlLeave uint16 = 4
)

const SyncReport uint16 = 0x00

const (
	RelX      uint16 = 0x00
	RelY      uint16 = 0x01
	RelHWheel uint16 = 0x06
	RelWheel  uint16 = 0x08
)

const (
	KeyReleased int32 = 0
	KeyPressed  int32 = 1
	KeyRepeated int32 = 2
)

const (
	KeyFirst uint16 = 1
	KeyA     uint16 = 30
	KeyLast  uint16 = 248
)

const (
	ButtonFirst  uint16 = 0x110
	ButtonLeft   uint16 = 0x110
	ButtonRight  uint16 = 0x111
	ButtonMiddle uint16 = 0x112
	ButtonLast   uint16 = 0x117
)

func Sync() Event {
	return Event{Type: TypeSync, Code: SyncReport}
}

func Key(code uint16, value int32) Event {
	return Event{Type: TypeKey, Code: code, Value: value}
}

func Relative(axis uint16, delta int32) Event {
	return Event{Type: TypeRelative, Code: axis, Value: delta}
}

func Hello() Event {
	return Event{Type: TypeControl, Code: ControlHello, Value: ProtocolVersion}
}

func Ping() Event {
	return Event{Type: TypeControl, Code: ControlPing}
}

func Enter() Event {
	return Event{Type: TypeControl, Code: ControlEnter}
}

func Leave() Event {
	return Event{Type: TypeControl, Code: ControlLeave}
}

func (e Event) IsControl() bool {
	return e.Type == TypeControl
}
