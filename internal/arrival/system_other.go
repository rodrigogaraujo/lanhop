//go:build !darwin && !linux

package arrival

import (
	"errors"
	"os"
)

const noFollow = 0

func freeSpaceOf(string) (int64, error) {
	return 0, errors.ErrUnsupported
}

func flush(file *os.File) error {
	return file.Sync()
}
