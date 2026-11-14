//go:build !unix

package filestream

import (
	"os"
	"path/filepath"
)

func openWithoutFollowing(path string) (*os.File, error) {
	return os.Open(path)
}

func openInside(_ *os.File, folderPath, name string) (*os.File, error) {
	return openWithoutFollowing(filepath.Join(folderPath, name))
}

func openedALink(error) bool {
	return false
}
