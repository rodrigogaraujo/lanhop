package filestream

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
)

const (
	pathBytesLimit   = 4096
	folderReadBatch  = 256
	entriesInAFolder = clipboard.FilesAndFoldersLimit + 1
)

var (
	ErrTooSlowToList = errors.New("filestream: the file copy could not be listed in the time an answer has")
	ErrNotSendable   = errors.New("filestream: the copy holds a link or a special file, and only files and folders are sent")
	ErrNotAbsolute   = errors.New("filestream: a path of the file copy is not absolute")
	ErrNoName        = errors.New("filestream: a path of the file copy has no name of its own")
	ErrSameName      = errors.New("filestream: two files or folders of the copy have the same name")
	ErrNameNotUTF8   = errors.New("filestream: a file name is not UTF-8")
	ErrPathTooLong   = errors.New("filestream: a path inside the copy is longer than 4096 bytes")
	ErrChanged       = errors.New("filestream: a file of the copy changed or went away before it was sent")
)

type Listing struct {
	Copy    clipboard.FileCopy
	Skipped int
}

type entry struct {
	path   string
	name   string
	folder bool
	info   fs.FileInfo
}

type lister struct {
	stop          func() error
	checkOpening  bool
	entries       []entry
	listing       Listing
	entriesLimit  int
	bytesLimit    int64
	folderEntries int
}

func Count(paths []string, deadline time.Time) (Listing, error) {
	l := newLister(func() error {
		if !deadline.IsZero() && time.Now().After(deadline) {
			return ErrTooSlowToList
		}
		return nil
	})
	l.checkOpening = true
	err := l.list(paths)
	return l.listing, err
}

func newLister(stop func() error) *lister {
	return &lister{
		stop:          stop,
		entriesLimit:  clipboard.FilesAndFoldersLimit,
		bytesLimit:    clipboard.FileCopyLimit,
		folderEntries: entriesInAFolder,
	}
}

func (l *lister) list(paths []string) error {
	if len(paths) == 0 {
		return clipboard.ErrNotFiles
	}
	names := map[string]bool{}
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			return fmt.Errorf("%w: %q", ErrNotAbsolute, path)
		}
		cleaned := filepath.Clean(path)
		name := filepath.Base(cleaned)
		if name == string(filepath.Separator) || name == "." || name == ".." {
			return fmt.Errorf("%w: %q", ErrNoName, path)
		}
		if names[name] {
			return fmt.Errorf("%w: %q", ErrSameName, name)
		}
		names[name] = true
		info, err := os.Lstat(cleaned)
		if err != nil {
			return fmt.Errorf("filestream: %w", err)
		}
		if !info.Mode().IsRegular() && !info.IsDir() {
			return fmt.Errorf("%w: %s", ErrNotSendable, cleaned)
		}
		if err := l.add(cleaned, name, info); err != nil {
			return err
		}
	}
	return nil
}

func (l *lister) add(path, name string, info fs.FileInfo) error {
	if err := l.stop(); err != nil {
		return err
	}
	if err := sendableName(name); err != nil {
		return fmt.Errorf("%w: %s", err, path)
	}
	l.listing.Copy.FilesAndFolders++
	if l.listing.Copy.FilesAndFolders > l.entriesLimit {
		return fmt.Errorf("%w: more than %d", clipboard.ErrTooManyFiles, l.entriesLimit)
	}
	l.entries = append(l.entries, entry{path: path, name: name, folder: info.IsDir(), info: info})
	if !info.IsDir() {
		l.listing.Copy.Bytes += info.Size()
		if l.listing.Copy.Bytes > l.bytesLimit {
			return fmt.Errorf("%w: more than %d bytes", clipboard.ErrFilesTooLarge, l.bytesLimit)
		}
		if l.checkOpening {
			return checkOpening(entry{path: path, name: name, info: info})
		}
		return nil
	}
	children, err := l.readFolder(path, info)
	if err != nil {
		return err
	}
	for _, child := range children {
		childInfo, err := child.Info()
		if err != nil {
			return fmt.Errorf("filestream: %w", err)
		}
		if !childInfo.Mode().IsRegular() && !childInfo.IsDir() {
			l.listing.Skipped++
			continue
		}
		if err := l.add(filepath.Join(path, child.Name()), name+"/"+child.Name(), childInfo); err != nil {
			return err
		}
	}
	return nil
}

func (l *lister) readFolder(path string, listed fs.FileInfo) ([]fs.DirEntry, error) {
	folder, err := openWithoutFollowing(path)
	if err != nil {
		return nil, openingFailed(path, err)
	}
	defer folder.Close()
	opened, err := folder.Stat()
	if err != nil {
		return nil, fmt.Errorf("filestream: %w", err)
	}
	if !opened.IsDir() || !os.SameFile(listed, opened) {
		return nil, fmt.Errorf("%w: %s", ErrChanged, path)
	}
	var children []fs.DirEntry
	for {
		if err := l.stop(); err != nil {
			return nil, err
		}
		batch, err := folder.ReadDir(folderReadBatch)
		children = append(children, batch...)
		if len(children) > l.folderEntries {
			return nil, fmt.Errorf("%w: more than %d in %s", clipboard.ErrTooManyFiles, l.entriesLimit, path)
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("filestream: %w", err)
		}
		if len(batch) == 0 {
			break
		}
	}
	slices.SortFunc(children, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	return children, nil
}

func checkOpening(e entry) error {
	file, err := openWithoutFollowing(e.path)
	if err != nil {
		return openingFailed(e.path, err)
	}
	defer file.Close()
	return stillAsListed(file, e)
}

func openingFailed(path string, err error) error {
	if errors.Is(err, fs.ErrNotExist) || openedALink(err) {
		return fmt.Errorf("%w: %s", ErrChanged, path)
	}
	return fmt.Errorf("filestream: %w", err)
}

func sameAsListed(listed, now fs.FileInfo) bool {
	return now.Mode().IsRegular() &&
		os.SameFile(listed, now) &&
		now.Size() == listed.Size() &&
		now.ModTime().Equal(listed.ModTime())
}

func sendableName(name string) error {
	if !utf8.ValidString(name) {
		return ErrNameNotUTF8
	}
	if len(name) > pathBytesLimit {
		return ErrPathTooLong
	}
	return nil
}
