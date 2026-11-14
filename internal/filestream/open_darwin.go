package filestream

import (
	"os"
	"path/filepath"
)

const noLinkAnywhereOnThePath = 0x20000000

func openWithoutFollowing(path string) (*os.File, error) {
	return os.OpenFile(path, readingWithoutFollowing|noLinkAnywhereOnThePath, 0)
}

func openInside(_ *os.File, folderPath, name string) (*os.File, error) {
	return openWithoutFollowing(filepath.Join(folderPath, name))
}
