package clipboard

import (
	"errors"
	"fmt"

	"github.com/rodrigogaraujo/lanhop/internal/features"
)

type Kinds uint8

const (
	Text  Kinds = 1 << features.Clipboard
	Files Kinds = 1 << features.Files
)

func KindsIn(featuresValue int32) Kinds {
	return Kinds(featuresValue) & (Text | Files)
}

func (k Kinds) Has(kind Kinds) bool {
	return k&kind == kind
}

const (
	FileCopyLimit        int64 = 4 << 30
	FilesAndFoldersLimit       = 10_000
)

type FileCopy struct {
	FilesAndFolders int
	Bytes           int64
}

var (
	ErrTooManyFiles   = errors.New("clipboard: the file copy holds more than 10 000 files and folders")
	ErrFilesTooLarge  = errors.New("clipboard: the file copy is larger than 4 GiB")
	ErrFilesNotShared = errors.New("clipboard: the copy holds files, and files are not shared with the other machine")
	ErrTextNotShared  = errors.New("clipboard: the copy is text, and text is not shared with the other machine")
	ErrNegativeFiles  = errors.New("clipboard: the file copy has a negative number of files or a negative size")
)

func sendableFiles(files FileCopy) error {
	switch {
	case files.FilesAndFolders < 0 || files.Bytes < 0:
		return fmt.Errorf("%w: %d files and folders, %d bytes", ErrNegativeFiles, files.FilesAndFolders, files.Bytes)
	case files.FilesAndFolders == 0:
		return ErrEmpty
	case files.FilesAndFolders > FilesAndFoldersLimit:
		return fmt.Errorf("%w: %d", ErrTooManyFiles, files.FilesAndFolders)
	case files.Bytes > FileCopyLimit:
		return fmt.Errorf("%w: %d bytes", ErrFilesTooLarge, files.Bytes)
	}
	return nil
}
