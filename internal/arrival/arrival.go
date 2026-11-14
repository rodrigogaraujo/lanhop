package arrival

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
)

var (
	ErrUnsafePath        = errors.New("arrival: a name that arrived is not a plain relative path inside the arrival")
	ErrImpossibleSize    = errors.New("arrival: a file announced a negative size")
	ErrMoreThanAnnounced = errors.New("arrival: more files, folders or bytes arrived than were announced")
	ErrSizeDiffers       = errors.New("arrival: a file did not hold the size it announced")
	ErrIncomplete        = errors.New("arrival: not every announced file and folder has arrived whole")
	ErrOutOfOrder        = errors.New("arrival: an entry came before Expect, after Finish, or while a file was not whole yet")
	ErrGivenUp           = errors.New("arrival: the arrival was given up, and what arrived of it was removed")
)

type Arrival struct {
	folder   *Folder
	transfer int
	written  atomic.Int64

	state         sync.Mutex
	name          string
	path          string
	bytes         int64
	entries       int
	expected      bool
	finished      bool
	gaveUp        bool
	entriesSoFar  int
	bytesSoFar    int64
	folders       map[string]fs.FileInfo
	files         map[string]bool
	open          *IncomingFile
	topLevelPaths []string
}

type IncomingFile struct {
	arrival   *Arrival
	file      *os.File
	relative  string
	elements  []string
	temporary string
	final     string
	topLevel  bool
	size      int64
	written   int64
}

func (a *Arrival) Expect(bytes int64, filesAndFolders int) error {
	a.state.Lock()
	defer a.state.Unlock()
	if err := a.mayExpect(); err != nil {
		return a.orGiveUp(err)
	}
	if err := a.folder.start(a, bytes, filesAndFolders); err != nil {
		return a.orGiveUp(err)
	}
	a.expected = true
	return nil
}

func (a *Arrival) Folder(name string) error {
	a.state.Lock()
	defer a.state.Unlock()
	return a.orGiveUp(a.makeFolder(name))
}

func (a *Arrival) File(name string, size int64) (*IncomingFile, error) {
	a.state.Lock()
	defer a.state.Unlock()
	incoming, err := a.openFile(name, size)
	if err != nil {
		return nil, a.orGiveUp(err)
	}
	return incoming, nil
}

func (a *Arrival) Finish() error {
	a.state.Lock()
	defer a.state.Unlock()
	return a.orGiveUp(a.finish())
}

func (a *Arrival) Paths() []string {
	a.state.Lock()
	defer a.state.Unlock()
	if !a.finished || a.gaveUp {
		return nil
	}
	return slices.Clone(a.topLevelPaths)
}

func (a *Arrival) GiveUp() {
	a.state.Lock()
	defer a.state.Unlock()
	a.giveUp()
}

func (i *IncomingFile) Write(content []byte) (int, error) {
	a := i.arrival
	a.state.Lock()
	defer a.state.Unlock()
	written, err := i.write(content)
	return written, a.orGiveUp(err)
}

func (i *IncomingFile) Whole() error {
	a := i.arrival
	a.state.Lock()
	defer a.state.Unlock()
	return a.orGiveUp(i.whole())
}

func (a *Arrival) orGiveUp(err error) error {
	if err != nil {
		a.giveUp()
	}
	return err
}

func (a *Arrival) giveUp() {
	if a.gaveUp {
		return
	}
	a.gaveUp = true
	if a.open != nil {
		a.open.file.Close()
		a.open = nil
	}
	if a.name == "" {
		return
	}
	if a.finished {
		a.folder.removeFinished(a.name)
		return
	}
	os.RemoveAll(a.path)
	a.folder.forget(a.name)
}

func (a *Arrival) mayExpect() error {
	switch {
	case a.gaveUp:
		return ErrGivenUp
	case a.expected:
		return fmt.Errorf("%w: Expect came twice", ErrOutOfOrder)
	}
	return nil
}

func (a *Arrival) mayTakeAnEntry() error {
	switch {
	case a.gaveUp:
		return ErrGivenUp
	case !a.expected || a.finished || a.open != nil:
		return ErrOutOfOrder
	case a.entriesSoFar >= a.entries:
		return fmt.Errorf("%w: %d files and folders were announced", ErrMoreThanAnnounced, a.entries)
	}
	return nil
}

func (a *Arrival) makeFolder(name string) error {
	if err := a.mayTakeAnEntry(); err != nil {
		return err
	}
	elements, full, err := a.placeFor(name)
	if err != nil {
		return err
	}
	if err := os.Mkdir(full, 0o700); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%w: %q is there already and this arrival did not make it: %w", ErrUnsafePath, name, err)
		}
		return fmt.Errorf("arrival: %w", err)
	}
	identity, err := os.Lstat(full)
	if err != nil {
		return fmt.Errorf("arrival: %w", err)
	}
	if err := markQuarantined(full, a.folder.config.now()); err != nil {
		return fmt.Errorf("arrival: the quarantine mark on %q: %w", name, err)
	}
	a.entriesSoFar++
	a.folders[name] = identity
	if len(elements) == 1 {
		a.topLevelPaths = append(a.topLevelPaths, full)
	}
	return nil
}

func (a *Arrival) openFile(name string, size int64) (*IncomingFile, error) {
	if err := a.mayTakeAnEntry(); err != nil {
		return nil, err
	}
	if size < 0 {
		return nil, fmt.Errorf("%w: %q with %d bytes", ErrImpossibleSize, name, size)
	}
	if size > a.bytes-a.bytesSoFar {
		return nil, fmt.Errorf("%w: %q has %d bytes, and only %d of the %d bytes announced are left", ErrMoreThanAnnounced, name, size, a.bytes-a.bytesSoFar, a.bytes)
	}
	elements, final, err := a.placeFor(name)
	if err != nil {
		return nil, err
	}
	if err := nothingAt(final, name); err != nil {
		return nil, err
	}
	temporary := filepath.Join(filepath.Dir(final), temporaryName(a.entriesSoFar+1))
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY|noFollow, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return nil, fmt.Errorf("%w: the temporary name of %q is there already: %w", ErrUnsafePath, name, err)
	}
	if err != nil {
		return nil, fmt.Errorf("arrival: %w", err)
	}
	a.open = &IncomingFile{arrival: a, file: file, relative: name, elements: elements, temporary: temporary, final: final, topLevel: len(elements) == 1, size: size}
	if err := markFileQuarantined(file, a.folder.config.now()); err != nil {
		return nil, fmt.Errorf("arrival: the quarantine mark on %q: %w", name, err)
	}
	a.entriesSoFar++
	a.bytesSoFar += size
	return a.open, nil
}

func (a *Arrival) placeFor(name string) ([]string, string, error) {
	elements, err := safeElements(name)
	if err != nil {
		return nil, "", err
	}
	if a.folders[name] != nil || a.files[name] {
		return nil, "", fmt.Errorf("%w: %q arrived already", ErrUnsafePath, name)
	}
	parent := strings.Join(elements[:len(elements)-1], "/")
	switch {
	case a.files[parent]:
		return nil, "", fmt.Errorf("%w: %q sits under a file", ErrUnsafePath, name)
	case a.folders[parent] == nil:
		return nil, "", fmt.Errorf("%w: %q sits in a folder that did not arrive", ErrUnsafePath, name)
	}
	full := filepath.Join(a.path, filepath.Join(elements...))
	if !strings.HasPrefix(full, a.path+string(filepath.Separator)) {
		return nil, "", fmt.Errorf("%w: %q leaves the arrival", ErrUnsafePath, name)
	}
	if err := a.foldersStillThere(elements); err != nil {
		return nil, "", err
	}
	return elements, full, nil
}

func (a *Arrival) foldersStillThere(elements []string) error {
	for depth := range len(elements) {
		relative := strings.Join(elements[:depth], "/")
		found, err := os.Lstat(filepath.Join(a.path, filepath.Join(elements[:depth]...)))
		if err != nil || !os.SameFile(found, a.folders[relative]) {
			return fmt.Errorf("%w: the folder %q is no longer the one this arrival made", ErrUnsafePath, relative)
		}
	}
	return nil
}

func safeElements(name string) ([]string, error) {
	refuse := func(why string) ([]string, error) {
		return nil, fmt.Errorf("%w: %q %s", ErrUnsafePath, name, why)
	}
	switch {
	case name == "":
		return refuse("is empty")
	case strings.ContainsRune(name, 0):
		return refuse("holds a NUL byte")
	case strings.ContainsRune(name, '\\'):
		return refuse("holds a backslash")
	case strings.HasPrefix(name, "/") || filepath.IsAbs(name):
		return refuse("is absolute")
	case filepath.Clean(name) != name:
		return refuse("is not clean")
	}
	elements := strings.Split(name, "/")
	for _, element := range elements {
		if element == "" || element == "." || element == ".." {
			return refuse("holds an empty, . or .. element")
		}
	}
	return elements, nil
}

func nothingAt(path, name string) error {
	switch _, err := os.Lstat(path); {
	case err == nil:
		return fmt.Errorf("%w: %q is there already and this arrival did not make it", ErrUnsafePath, name)
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("arrival: %w", err)
	}
	return nil
}

func (i *IncomingFile) write(content []byte) (int, error) {
	a := i.arrival
	switch {
	case a.gaveUp:
		return 0, ErrGivenUp
	case a.open != i:
		return 0, fmt.Errorf("%w: %q is not the file being written", ErrOutOfOrder, i.relative)
	case int64(len(content)) > i.size-i.written:
		return 0, fmt.Errorf("%w: %q has %d bytes, and more arrived", ErrSizeDiffers, i.relative, i.size)
	}
	written, err := i.file.Write(content)
	i.written += int64(written)
	a.written.Add(int64(written))
	if err != nil {
		return written, fmt.Errorf("arrival: %w", err)
	}
	return written, nil
}

func (i *IncomingFile) whole() error {
	a := i.arrival
	switch {
	case a.gaveUp:
		return ErrGivenUp
	case a.open != i:
		return fmt.Errorf("%w: %q is not the file being written", ErrOutOfOrder, i.relative)
	case i.written != i.size:
		return fmt.Errorf("%w: %q got %d of its %d bytes", ErrSizeDiffers, i.relative, i.written, i.size)
	}
	info, err := i.file.Stat()
	if err != nil {
		return fmt.Errorf("arrival: %w", err)
	}
	if info.Size() != i.size {
		return fmt.Errorf("%w: %q holds %d bytes on the disk, %d were announced", ErrSizeDiffers, i.relative, info.Size(), i.size)
	}
	if err := flush(i.file); err != nil {
		return fmt.Errorf("arrival: %w", err)
	}
	a.open = nil
	if err := i.file.Close(); err != nil {
		return fmt.Errorf("arrival: %w", err)
	}
	if err := a.foldersStillThere(i.elements); err != nil {
		return err
	}
	if before := a.folder.config.beforePlacing; before != nil {
		before(i.final)
	}
	if err := placeWithoutReplacing(i.temporary, i.final, i.relative); err != nil {
		return err
	}
	a.files[i.relative] = true
	if i.topLevel {
		a.topLevelPaths = append(a.topLevelPaths, i.final)
	}
	return nil
}

func placeWithoutReplacing(temporary, final, name string) error {
	if err := os.Link(temporary, final); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%w: %q is there already and this arrival did not make it: %w", ErrUnsafePath, name, err)
		}
		return fmt.Errorf("arrival: placing %q without replacing anything needs a hard link, and the disk refused it: %w", name, err)
	}
	if err := os.Remove(temporary); err != nil {
		return fmt.Errorf("arrival: %w", err)
	}
	return nil
}

func (a *Arrival) finish() error {
	switch {
	case a.gaveUp:
		return ErrGivenUp
	case a.finished:
		return nil
	case !a.expected:
		return ErrOutOfOrder
	case a.open != nil || a.entriesSoFar != a.entries || a.bytesSoFar != a.bytes:
		return fmt.Errorf("%w: %d of %d files and folders are whole", ErrIncomplete, len(a.folders)-1+len(a.files), a.entries)
	}
	if err := a.folder.writeRecord(a.name, a.folder.config.now(), a.bytes); err != nil {
		return err
	}
	a.finished = true
	a.folder.forget(a.name)
	return nil
}

func temporaryName(position int) string {
	return fmt.Sprintf(".lanhop-arriving-%d", position)
}
