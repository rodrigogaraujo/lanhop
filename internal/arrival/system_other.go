//go:build !darwin && !linux

package arrival

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

const noFollow = 0

func freeSpaceOf(string) (int64, error) {
	return 0, errors.ErrUnsupported
}

func flush(file *os.File) error {
	return file.Sync()
}

func ownedByThisUser(fs.FileInfo) bool {
	return true
}

func lockFolder(path string) (*os.File, error) {
	lock, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("arrival: %w", err)
	}
	return lock, nil
}
