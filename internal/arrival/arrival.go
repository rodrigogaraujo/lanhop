package arrival

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
)

type Kind uint8

const (
	FileKind   Kind = 1
	FolderKind Kind = 2
)

type Entry struct {
	Path string
	Kind Kind
	Size int64
}

var (
	ErrUnsafePath        = errors.New("arrival: a path that arrived is not a plain relative path inside the arrival")
	ErrNotFileOrFolder   = errors.New("arrival: an entry that arrived is neither a file nor a folder")
	ErrImpossibleSize    = errors.New("arrival: an entry announced a size it cannot have")
	ErrMoreThanAnnounced = errors.New("arrival: more files, folders or bytes arrived than were announced")
	ErrSizeDiffers       = errors.New("arrival: a file did not hold the size it announced")
	ErrDigestDiffers     = errors.New("arrival: a file did not match its SHA-256")
	ErrIncomplete        = errors.New("arrival: not every announced file and folder has arrived")
	ErrStopped           = errors.New("arrival: the arrival stopped, and what arrived of it was removed")
)

type made uint8

const (
	madeFolder made = iota + 1
	madeFile
)

type Arrival struct {
	folder  *Folder
	name    string
	path    string
	bytes   int64
	entries int

	receiving     sync.Mutex
	entriesSoFar  int
	bytesSoFar    int64
	made          map[string]made
	topLevelPaths []string

	state     sync.Mutex
	completed bool
	stopped   atomic.Bool
	written   atomic.Int64
}

func (a *Arrival) Receive(entry Entry, from io.Reader) error {
	a.receiving.Lock()
	defer a.receiving.Unlock()
	if err := a.receive(entry, from); err != nil {
		a.Stop()
		return err
	}
	return nil
}

func (a *Arrival) Complete() ([]string, error) {
	a.receiving.Lock()
	defer a.receiving.Unlock()
	if a.isCompleted() {
		return slices.Clone(a.topLevelPaths), nil
	}
	if err := a.complete(); err != nil {
		a.Stop()
		return nil, err
	}
	a.folder.forget(a.name)
	return slices.Clone(a.topLevelPaths), nil
}

func (a *Arrival) Stop() error {
	a.state.Lock()
	if a.completed || a.stopped.Load() {
		a.state.Unlock()
		return nil
	}
	a.stopped.Store(true)
	err := os.RemoveAll(a.path)
	a.state.Unlock()
	a.folder.forget(a.name)
	if err != nil {
		return fmt.Errorf("arrival: %w", err)
	}
	return nil
}

func (a *Arrival) isCompleted() bool {
	a.state.Lock()
	defer a.state.Unlock()
	return a.completed
}

func (a *Arrival) whileWriting(write func() error) error {
	a.state.Lock()
	defer a.state.Unlock()
	if a.stopped.Load() {
		return ErrStopped
	}
	return write()
}

func (a *Arrival) receive(entry Entry, from io.Reader) error {
	if a.stopped.Load() {
		return ErrStopped
	}
	if entry.Kind != FileKind && entry.Kind != FolderKind {
		return fmt.Errorf("%w: %q is of kind %d", ErrNotFileOrFolder, entry.Path, entry.Kind)
	}
	if entry.Size < 0 || (entry.Kind == FolderKind && entry.Size != 0) {
		return fmt.Errorf("%w: %q with %d bytes", ErrImpossibleSize, entry.Path, entry.Size)
	}
	elements, err := safeElements(entry.Path)
	if err != nil {
		return err
	}
	if a.entriesSoFar >= a.entries || entry.Size > a.bytes-a.bytesSoFar {
		return fmt.Errorf("%w: %d files and folders and %d bytes were announced", ErrMoreThanAnnounced, a.entries, a.bytes)
	}
	a.entriesSoFar++
	a.bytesSoFar += entry.Size
	for depth := 1; depth < len(elements); depth++ {
		if err := a.makeFolder(elements[:depth]); err != nil {
			return err
		}
	}
	if entry.Kind == FolderKind {
		return a.makeFolder(elements)
	}
	return a.receiveFile(elements, entry.Size, from)
}

func safeElements(path string) ([]string, error) {
	refuse := func(why string) ([]string, error) {
		return nil, fmt.Errorf("%w: %q %s", ErrUnsafePath, path, why)
	}
	switch {
	case path == "":
		return refuse("is empty")
	case strings.ContainsRune(path, 0):
		return refuse("holds a NUL byte")
	case strings.ContainsRune(path, '\\'):
		return refuse("holds a backslash")
	case strings.HasPrefix(path, "/") || filepath.IsAbs(path):
		return refuse("is absolute")
	case filepath.Clean(path) != path:
		return refuse("is not clean")
	}
	elements := strings.Split(path, "/")
	for _, element := range elements {
		if element == "" || element == "." || element == ".." {
			return refuse("holds an empty, . or .. element")
		}
	}
	return elements, nil
}

func (a *Arrival) pathOf(elements []string) (string, error) {
	full := filepath.Join(a.path, filepath.Join(elements...))
	if !strings.HasPrefix(full, a.path+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q leaves the arrival", ErrUnsafePath, strings.Join(elements, "/"))
	}
	return full, nil
}

func (a *Arrival) makeFolder(elements []string) error {
	relative := strings.Join(elements, "/")
	switch a.made[relative] {
	case madeFolder:
		return nil
	case madeFile:
		return fmt.Errorf("%w: %q is a file that arrived already", ErrUnsafePath, relative)
	}
	full, err := a.pathOf(elements)
	if err != nil {
		return err
	}
	err = a.whileWriting(func() error {
		if err := os.Mkdir(full, 0o700); err != nil {
			if errors.Is(err, fs.ErrExist) {
				return fmt.Errorf("%w: %q is there already and this arrival did not make it: %w", ErrUnsafePath, relative, err)
			}
			return fmt.Errorf("arrival: %w", err)
		}
		return a.markQuarantined(full, relative)
	})
	if err != nil {
		return err
	}
	a.made[relative] = madeFolder
	if len(elements) == 1 {
		a.topLevelPaths = append(a.topLevelPaths, full)
	}
	return nil
}

func (a *Arrival) receiveFile(elements []string, size int64, from io.Reader) error {
	relative := strings.Join(elements, "/")
	if a.made[relative] != 0 {
		return fmt.Errorf("%w: %q arrived already", ErrUnsafePath, relative)
	}
	final, err := a.pathOf(elements)
	if err != nil {
		return err
	}
	temporary := filepath.Join(filepath.Dir(final), temporaryName(a.entriesSoFar))
	var file *os.File
	err = a.whileWriting(func() error {
		created, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY|noFollow, 0o600)
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%w: the temporary name of %q is there already: %w", ErrUnsafePath, relative, err)
		}
		if err != nil {
			return fmt.Errorf("arrival: %w", err)
		}
		file = created
		return a.markQuarantined(temporary, relative)
	})
	if err == nil {
		err = a.writeWhole(file, relative, size, from)
	}
	if file != nil {
		err = errors.Join(err, file.Close())
	}
	if err == nil {
		err = a.whileWriting(func() error { return putInPlace(temporary, final, relative) })
	}
	if err != nil {
		if file != nil {
			os.Remove(temporary)
		}
		return err
	}
	a.made[relative] = madeFile
	if len(elements) == 1 {
		a.topLevelPaths = append(a.topLevelPaths, final)
	}
	return nil
}

func (a *Arrival) writeWhole(file *os.File, relative string, size int64, from io.Reader) error {
	content := &contentWriter{file: file, digest: sha256.New(), arrival: a}
	if _, err := io.CopyN(content, from, size); err != nil {
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("%w: %q ended before its %d bytes", ErrSizeDiffers, relative, size)
		}
		return fmt.Errorf("arrival: %q: %w", relative, err)
	}
	var announced [sha256.Size]byte
	if _, err := io.ReadFull(from, announced[:]); err != nil {
		return fmt.Errorf("arrival: the SHA-256 of %q: %w", relative, err)
	}
	written, err := file.Stat()
	if err != nil {
		return fmt.Errorf("arrival: %w", err)
	}
	if written.Size() != size {
		return fmt.Errorf("%w: %q holds %d bytes, %d were announced", ErrSizeDiffers, relative, written.Size(), size)
	}
	if !bytes.Equal(content.digest.Sum(nil), announced[:]) {
		return fmt.Errorf("%w: %q", ErrDigestDiffers, relative)
	}
	if err := flush(file); err != nil {
		return fmt.Errorf("arrival: %w", err)
	}
	return nil
}

func putInPlace(temporary, final, relative string) error {
	switch _, err := os.Lstat(final); {
	case err == nil:
		return fmt.Errorf("%w: %q is there already and this arrival did not make it", ErrUnsafePath, relative)
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("arrival: %w", err)
	}
	if err := os.Rename(temporary, final); err != nil {
		return fmt.Errorf("arrival: %w", err)
	}
	return nil
}

func (a *Arrival) markQuarantined(path, relative string) error {
	if err := markQuarantined(path, a.folder.config.now()); err != nil {
		return fmt.Errorf("arrival: the quarantine mark on %q: %w", relative, err)
	}
	return nil
}

func (a *Arrival) complete() error {
	if a.stopped.Load() {
		return ErrStopped
	}
	if a.entriesSoFar != a.entries || a.bytesSoFar != a.bytes {
		return fmt.Errorf("%w: %d of %d files and folders, %d of %d bytes", ErrIncomplete, a.entriesSoFar, a.entries, a.bytesSoFar, a.bytes)
	}
	return a.whileWriting(func() error {
		if err := a.folder.writeRecord(a.name, a.folder.config.now(), a.bytes); err != nil {
			return err
		}
		a.completed = true
		return nil
	})
}

func temporaryName(entry int) string {
	return fmt.Sprintf(".lanhop-arriving-%d", entry)
}

type contentWriter struct {
	file    *os.File
	digest  hash.Hash
	arrival *Arrival
}

func (w *contentWriter) Write(content []byte) (int, error) {
	if w.arrival.stopped.Load() {
		return 0, ErrStopped
	}
	written, err := w.file.Write(content)
	w.digest.Write(content[:written])
	w.arrival.written.Add(int64(written))
	return written, err
}
