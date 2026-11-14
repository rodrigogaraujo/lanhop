//go:build !darwin

package arrival

import (
	"os"
	"time"
)

func quarantineReady() error {
	return nil
}

func markQuarantined(string, time.Time) error {
	return nil
}

func markFileQuarantined(*os.File, time.Time) error {
	return nil
}
