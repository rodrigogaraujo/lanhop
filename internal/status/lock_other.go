//go:build !(darwin || linux || freebsd || openbsd || netbsd || dragonfly)

package status

import (
	"fmt"
	"os"
)

func takeLock(path string) (*os.File, error) {
	lock, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("status: %w", err)
	}
	return lock, nil
}

func lockIsTaken(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
