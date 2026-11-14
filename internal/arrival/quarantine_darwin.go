//go:build darwin && cgo

package arrival

import (
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/macos"
)

func quarantineReady() error {
	return nil
}

func markQuarantined(path string, at time.Time) error {
	return macos.SetQuarantine(path, quarantineValue(at))
}
