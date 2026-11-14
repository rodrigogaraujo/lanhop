//go:build darwin && !cgo

package arrival

import "time"

func quarantineReady() error {
	return ErrCannotQuarantine
}

func markQuarantined(string, time.Time) error {
	return ErrCannotQuarantine
}
