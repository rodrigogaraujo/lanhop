//go:build darwin && !cgo

package arrival

import (
	"os"
	"time"
)

func quarantineReady() error {
	return ErrCannotQuarantine
}

func markQuarantined(string, time.Time) error {
	return ErrCannotQuarantine
}

func markFileQuarantined(*os.File, time.Time) error {
	return ErrCannotQuarantine
}
