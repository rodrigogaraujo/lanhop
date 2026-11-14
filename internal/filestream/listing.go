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
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
)

const (
	pathBytesLimit  = 4096
	folderReadBatch = 256
)

var (
	ErrTooSlowToList = errors.New("filestream: the file copy could not be listed in the time an answer has")
	ErrNotSendable   = errors.New("filestream: the copy holds a link or a special file, and only files and folders are sent")
	ErrNotAbsolute   = errors.New("filestream: a path of the file copy is not absolute")
	ErrNoName        = errors.New("filestream: a path of the file copy has no name of its own")
	ErrSameName      = errors.New("filestream: two files or folders of the copy have the same name")
	ErrNameNotUTF8   = errors.New("filestream: a file name is not UTF-8")
	ErrPathTooLong   = errors.New("filestream: a path of the copy is 4096 bytes or longer")
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
	stop         func() error
	entries      []entry
	listing      Listing
	entriesLimit int
	bytesLimit   int64
	pathLimit    int
}

type counted struct {
	listing Listing
	err     error
}

func Count(paths []string, deadline time.Time) (Listing, error) {
	l := newLister(func() error {
		if !deadline.IsZero() && time.Now().After(deadline) {
			return ErrTooSlowToList
		}
		return nil
	})
	done := make(chan counted, 1)
	paths = slices.Clone(paths)
	go func() {
		err := l.list(paths)
		done <- counted{listing: l.listing, err: err}
	}()
	if deadline.IsZero() {
		result := <-done
		return result.listing, result.err
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case result := <-done:
		return result.listing, result.err
	case <-timer.C:
		return Listing{}, ErrTooSlowToList
	}
}

func newLister(stop func() error) *lister {
	return &lister{
		stop:         stop,
		entriesLimit: clipboard.FilesAndFoldersLimit,
		bytesLimit:   clipboard.FileCopyLimit,
		pathLimit:    pathBytesLimit,
	}
}

func (l *lister) list(paths []string) error {
	if len(paths) == 0 {
		return clipboard.ErrNotFiles
	}
	names := map[string]bool{}
	for _, path := range paths {
		top, err := resolvedOnTheWay(path)
		if err != nil {
			return err
		}
		name := filepath.Base(top)
		if names[name] {
			return fmt.Errorf("%w: %q", ErrSameName, name)
		}
		names[name] = true
		if err := l.addTop(top, name); err != nil {
			return err
		}
	}
	return nil
}

func resolvedOnTheWay(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%w: %q", ErrNotAbsolute, path)
	}
	trimmed := strings.TrimRight(path, string(filepath.Separator))
	if trimmed == "" {
		return "", fmt.Errorf("%w: %q", ErrNoName, path)
	}
	last := trimmed[strings.LastIndexByte(trimmed, filepath.Separator)+1:]
	var resolved string
	if last == "." || last == ".." {
		folder, err := filepath.EvalSymlinks(trimmed)
		if err != nil {
			return "", fmt.Errorf("filestream: %w", err)
		}
		resolved = folder
	} else {
		folder, err := filepath.EvalSymlinks(trimmed[:len(trimmed)-len(last)])
		if err != nil {
			return "", fmt.Errorf("filestream: %w", err)
		}
		resolved = filepath.Join(folder, last)
	}
	if name := filepath.Base(resolved); name == string(filepath.Separator) || name == "." || name == ".." {
		return "", fmt.Errorf("%w: %q", ErrNoName, path)
	}
	return resolved, nil
}

func (l *lister) addTop(top, name string) error {
	listed, err := os.Lstat(top)
	if err != nil {
		return fmt.Errorf("filestream: %w", err)
	}
	if !listed.Mode().IsRegular() && !listed.IsDir() {
		return fmt.Errorf("%w: %s", ErrNotSendable, top)
	}
	file, err := openWithoutFollowing(top)
	if err != nil {
		return openingFailed(top, err)
	}
	opened, err := file.Stat()
	if err != nil {
		file.Close()
		return fmt.Errorf("filestream: %w", err)
	}
	if !os.SameFile(listed, opened) {
		file.Close()
		return fmt.Errorf("%w: %s", ErrChanged, top)
	}
	return l.add(file, top, name, opened)
}

func (l *lister) add(file *os.File, path, name string, info fs.FileInfo) error {
	defer file.Close()
	if err := l.stop(); err != nil {
		return err
	}
	if err := sendableName(name); err != nil {
		return fmt.Errorf("%w: %s", err, path)
	}
	if len(path) >= l.pathLimit {
		return fmt.Errorf("%w: %.80s…", ErrPathTooLong, path)
	}
	if !info.Mode().IsRegular() && !info.IsDir() {
		return fmt.Errorf("%w: %s", ErrChanged, path)
	}
	l.listing.Copy.FilesAndFolders++
	if l.listing.Copy.FilesAndFolders > l.entriesLimit {
		return fmt.Errorf("%w: more than %d", clipboard.ErrTooManyFiles, l.entriesLimit)
	}
	l.entries = append(l.entries, entry{path: path, name: name, folder: info.IsDir(), info: info})
	if !info.IsDir() {
		if info.Size() > l.bytesLimit-l.listing.Copy.Bytes {
			return fmt.Errorf("%w: more than %d bytes", clipboard.ErrFilesTooLarge, l.bytesLimit)
		}
		l.listing.Copy.Bytes += info.Size()
		return nil
	}
	children, err := l.readFolder(file, path)
	if err != nil {
		return err
	}
	for _, child := range children {
		childPath := filepath.Join(path, child)
		childFile, err := openInside(file, path, child)
		if err != nil {
			return openingFailed(childPath, err)
		}
		childInfo, err := childFile.Stat()
		if err != nil {
			childFile.Close()
			return fmt.Errorf("filestream: %w", err)
		}
		if err := l.add(childFile, childPath, name+"/"+child, childInfo); err != nil {
			return err
		}
	}
	return nil
}

func (l *lister) readFolder(folder *os.File, path string) ([]string, error) {
	room := l.entriesLimit - l.listing.Copy.FilesAndFolders
	var children []string
	for {
		if err := l.stop(); err != nil {
			return nil, err
		}
		batch, err := folder.ReadDir(folderReadBatch)
		for _, child := range batch {
			if kind := child.Type(); kind.IsRegular() || kind.IsDir() {
				children = append(children, child.Name())
			} else {
				l.listing.Skipped++
			}
		}
		if len(children) > room {
			return nil, fmt.Errorf("%w: more than %d", clipboard.ErrTooManyFiles, l.entriesLimit)
		}
		if errors.Is(err, io.EOF) || (err == nil && len(batch) == 0) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("filestream: reading %s: %w", path, err)
		}
	}
	slices.Sort(children)
	return children, nil
}

func openingFailed(path string, err error) error {
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) || openedALink(err) {
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
	if len(name) >= pathBytesLimit {
		return ErrPathTooLong
	}
	return nil
}
