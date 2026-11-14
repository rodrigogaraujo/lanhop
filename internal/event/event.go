package event

import "math"

type Event struct {
	Type  uint16
	Code  uint16
	Value int32
}

const (
	TypeSync     uint16 = 0x00
	TypeKey      uint16 = 0x01
	TypeRelative uint16 = 0x02
	TypeAbsolute uint16 = 0x03
	TypeControl  uint16 = 0xFF00
)

const ProtocolVersion int32 = 1

const (
	ControlHello uint16 = 1
	ControlPing  uint16 = 2
	ControlEnter uint16 = 3
	ControlLeave uint16 = 4

	ControlFeatures      uint16 = 5
	ControlClipboard     uint16 = 6
	ControlClipboardData uint16 = 7
	ControlClipboardAck  uint16 = 8

	ControlClipboardUnchanged uint16 = 9

	ControlDataHello    uint16 = 10
	ControlFileCopy     uint16 = 11
	ControlFileCopySize uint16 = 12
	ControlOpenData     uint16 = 13
	ControlDataStop     uint16 = 14
	ControlDataAck      uint16 = 15

	ControlUpdate  uint16 = 16
	ControlVersion uint16 = 17
)

const FeatureClipboard int32 = 1 << 0

const (
	TransferNumberMax   int32 = math.MaxInt32
	sideMachineSendsBit int32 = math.MinInt32
)

const SyncReport uint16 = 0x00

const (
	RelX      uint16 = 0x00
	RelY      uint16 = 0x01
	RelHWheel uint16 = 0x06
	RelWheel  uint16 = 0x08
)

const (
	AbsX uint16 = 0x00
	AbsY uint16 = 0x01
)

const AbsoluteMax int32 = 65535

const (
	KeyReleased int32 = 0
	KeyPressed  int32 = 1
	KeyRepeated int32 = 2
)

const (
	KeyFirst uint16 = 1
	KeyEnter uint16 = 28
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

func Absolute(axis uint16, position int32) Event {
	return Event{Type: TypeAbsolute, Code: axis, Value: position}
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

func Features(set int32) Event {
	return Event{Type: TypeControl, Code: ControlFeatures, Value: set}
}

func Update(release int32) Event {
	return Event{Type: TypeControl, Code: ControlUpdate, Value: release}
}

func Version(release int32) Event {
	return Event{Type: TypeControl, Code: ControlVersion, Value: release}
}

func Clipboard(length int32) Event {
	return Event{Type: TypeControl, Code: ControlClipboard, Value: length}
}

func ClipboardData(chunk int32) Event {
	return Event{Type: TypeControl, Code: ControlClipboardData, Value: chunk}
}

func ClipboardAck(framesRead int32) Event {
	return Event{Type: TypeControl, Code: ControlClipboardAck, Value: framesRead}
}

func ClipboardUnchanged() Event {
	return Event{Type: TypeControl, Code: ControlClipboardUnchanged}
}

func DataHello(transfer int32) Event {
	return Event{Type: TypeControl, Code: ControlDataHello, Value: transfer}
}

func FileCopy(filesAndFolders int32) Event {
	return Event{Type: TypeControl, Code: ControlFileCopy, Value: filesAndFolders}
}

func FileCopySize(kibibytesRoundedUp int32) Event {
	return Event{Type: TypeControl, Code: ControlFileCopySize, Value: kibibytesRoundedUp}
}

func OpenData(transfer int32, sideMachineSends bool) Event {
	value := transfer & TransferNumberMax
	if sideMachineSends {
		value |= sideMachineSendsBit
	}
	return Event{Type: TypeControl, Code: ControlOpenData, Value: value}
}

func SplitOpenData(value int32) (transfer int32, sideMachineSends bool) {
	return value & TransferNumberMax, value&sideMachineSendsBit != 0
}

func DataStop(transfer int32) Event {
	return Event{Type: TypeControl, Code: ControlDataStop, Value: transfer}
}

const everyFileArrivedBit int32 = math.MinInt32

func DataAck(blocksRead int32) Event {
	return Event{Type: TypeControl, Code: ControlDataAck, Value: blocksRead & TransferNumberMax}
}

func AllArrived(blocksRead int32) Event {
	return Event{Type: TypeControl, Code: ControlDataAck, Value: blocksRead&TransferNumberMax | everyFileArrivedBit}
}

func SplitDataAck(value int32) (blocksRead int32, allArrived bool) {
	return value & TransferNumberMax, value&everyFileArrivedBit != 0
}

func (e Event) IsControl() bool {
	return e.Type == TypeControl
}
