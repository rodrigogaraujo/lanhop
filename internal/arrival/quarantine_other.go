//go:build !darwin

package arrival

import "time"

func quarantineReady() error {
	return nil
}

func markQuarantined(string, time.Time) error {
	return nil
}
