//go:build unix && !linux && !darwin

package filestream

import (
	"os"
	"path/filepath"
)

func openWithoutFollowing(path string) (*os.File, error) {
	return os.OpenFile(path, readingWithoutFollowing, 0)
}

func openInside(_ *os.File, folderPath, name string) (*os.File, error) {
	return openWithoutFollowing(filepath.Join(folderPath, name))
}
