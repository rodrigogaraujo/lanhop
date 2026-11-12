//go:build linux && (386 || amd64 || arm || arm64 || loong64 || riscv64)

package evdev

import (
	"syscall"
	"unsafe"

	"github.com/rodrigogaraujo/vaivem/internal/event"
)

type rawEvent struct {
	time  syscall.Timeval
	kind  uint16
	code  uint16
	value int32
}

const rawEventSize = int(unsafe.Sizeof(rawEvent{}))

func toRaw(e event.Event) rawEvent {
	return rawEvent{kind: e.Type, code: e.Code, value: e.Value}
}

func (r rawEvent) toEvent() event.Event {
	return event.Event{Type: r.kind, Code: r.code, Value: r.value}
}
