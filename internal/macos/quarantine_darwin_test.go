//go:build darwin && cgo

package macos

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestTheQuarantineMarkReadsBackAsItWasSetOnAFileAndAFolder(t *testing.T) {
	folder := t.TempDir()
	file := filepath.Join(folder, "report.pdf")
	if err := os.WriteFile(file, []byte("report"), 0o600); err != nil {
		t.Fatal(err)
	}
	const value = "0081;6734a1c0;lanhop;"
	for _, path := range []string{file, folder} {
		if err := SetQuarantine(path, value); err != nil {
			t.Fatalf("SetQuarantine(%s): %v", path, err)
		}
		if got, err := Quarantine(path); err != nil || got != value {
			t.Errorf("Quarantine(%s) = %q, %v, want %q", path, got, err, value)
		}
	}
}

func TestAFileWithoutTheQuarantineMarkSaysSo(t *testing.T) {
	file := filepath.Join(t.TempDir(), "plain.txt")
	if err := os.WriteFile(file, []byte("plain"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := Quarantine(file); !errors.Is(err, syscall.ENOATTR) {
		t.Errorf("Quarantine of a file never marked = %q, %v, want ENOATTR", got, err)
	}
}

func TestTheQuarantineMarkSetThroughAnOpenFileReadsBack(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "arriving")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	const value = "0081;6734a1c0;lanhop;"
	if err := SetQuarantineOn(file.Fd(), value); err != nil {
		t.Fatalf("SetQuarantineOn: %v", err)
	}
	if got, err := Quarantine(file.Name()); err != nil || got != value {
		t.Errorf("Quarantine = %q, %v, want %q", got, err, value)
	}
}
