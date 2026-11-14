//go:build unix

package filestream

import (
	"errors"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestCountSkipsAPipeInsideAFolderAndRefusesACopyOfOne(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "project", "main.go"), "package main")
	pipe := filepath.Join(root, "project", "pipe")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Fatal(err)
	}

	listing, err := Count([]string{filepath.Join(root, "project")}, time.Time{})
	if err != nil || listing.Skipped != 1 || listing.Copy.FilesAndFolders != 2 {
		t.Errorf("Count of a folder holding a pipe = %+v, %v, want the folder and its file, and 1 skipped", listing, err)
	}

	if _, err := Count([]string{pipe}, time.Time{}); !errors.Is(err, ErrNotSendable) {
		t.Errorf("Count of a pipe = %v, want ErrNotSendable", err)
	}
}
