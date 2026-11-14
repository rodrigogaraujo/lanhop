//go:build darwin && cgo

package arrival

import (
	"os"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/macos"
)

func quarantineReady() error {
	return nil
}

func markQuarantined(path string, at time.Time) error {
	return macos.SetQuarantine(path, quarantineValue(at))
}

func markFileQuarantined(file *os.File, at time.Time) error {
	raw, err := file.SyscallConn()
	if err != nil {
		return err
	}
	var marked error
	if err := raw.Control(func(descriptor uintptr) { marked = macos.SetQuarantineOn(descriptor, quarantineValue(at)) }); err != nil {
		return err
	}
	return marked
}
