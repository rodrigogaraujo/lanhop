//go:build unix

package filestream

import (
	"errors"
	"os"
	"syscall"
)

const readingWithoutFollowing = os.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK

func openedALink(err error) bool {
	return errors.Is(err, syscall.ELOOP) || errors.Is(err, syscall.EMLINK)
}
