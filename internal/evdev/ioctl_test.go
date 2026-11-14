//go:build linux && (386 || amd64 || arm || arm64 || loong64 || riscv64)

package evdev

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func openTemporary(t *testing.T) *os.File {
	t.Helper()
	file, err := os.Create(filepath.Join(t.TempDir(), "device"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })
	return file
}

func TestACallInterruptedByASignalIsMadeAgain(t *testing.T) {
	interruptions := 3
	calls := 0
	err := withDescriptor(openTemporary(t), func(uintptr) syscall.Errno {
		calls++
		if calls <= interruptions {
			return syscall.EINTR
		}
		return 0
	})

	if err != nil {
		t.Errorf("the call gave %v after %d interruptions, want it made again until it succeeds", err, interruptions)
	}
	if calls != interruptions+1 {
		t.Errorf("the call was made %d times, want %d", calls, interruptions+1)
	}
}

func TestACallThatFailsForAnotherReasonIsNotMadeAgain(t *testing.T) {
	calls := 0
	err := withDescriptor(openTemporary(t), func(uintptr) syscall.Errno {
		calls++
		return syscall.EBUSY
	})

	if !errors.Is(err, syscall.EBUSY) {
		t.Errorf("the call gave %v, want EBUSY", err)
	}
	if calls != 1 {
		t.Errorf("the call was made %d times, want once", calls)
	}
}
