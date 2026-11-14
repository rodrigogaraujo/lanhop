//go:build !unix

package filestream

import "os"

func openWithoutFollowing(path string) (*os.File, error) {
	return os.Open(path)
}

func openedALink(error) bool {
	return false
}
