package arrival

import (
	"cmp"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
)

const (
	DefaultRoom   int64 = 8 << 30
	DefaultMargin int64 = 256 << 20
	KeptFor             = 24 * time.Hour

	folderName   = "arrivals"
	recordSuffix = ".complete"
)

var (
	ErrNoRoom           = errors.New("arrival: the files do not fit in the room lanhop allows itself, or in the free space of the disk")
	ErrOutsideTheLimits = errors.New("arrival: a file copy holds from 1 to 10 000 files and folders, and at most 4 GiB")
	ErrCannotQuarantine = errors.New("arrival: this lanhop was built without cgo, so files that arrive cannot get the quarantine mark of macOS")
	errUnreadableRecord = errors.New("arrival: the record of a completed arrival cannot be read")
)

type Config struct {
	Directory string
	Room      int64
	Margin    int64

	now       func() time.Time
	freeSpace func(directory string) (int64, error)
}

func (c Config) withDefaults() (Config, error) {
	if c.Directory == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return c, fmt.Errorf("arrival: %w", err)
		}
		c.Directory = filepath.Join(cache, "lanhop", folderName)
	}
	directory, err := filepath.Abs(c.Directory)
	if err != nil {
		return c, fmt.Errorf("arrival: %w", err)
	}
	c.Directory = directory
	if c.Room == 0 {
		c.Room = DefaultRoom
	}
	if c.Margin == 0 {
		c.Margin = DefaultMargin
	}
	if c.now == nil {
		c.now = time.Now
	}
	if c.freeSpace == nil {
		c.freeSpace = freeSpaceOf
	}
	return c, nil
}

type Folder struct {
	config  Config
	mutex   sync.Mutex
	writing map[string]*Arrival
}

type completedArrival struct {
	name      string
	completed time.Time
	bytes     int64
}

func Open(config Config) (*Folder, error) {
	if err := quarantineReady(); err != nil {
		return nil, err
	}
	config, err := config.withDefaults()
	if err != nil {
		return nil, err
	}
	return &Folder{config: config, writing: make(map[string]*Arrival)}, nil
}

func (f *Folder) Directory() string {
	return f.config.Directory
}

func (f *Folder) Expecting(transfer int) *Arrival {
	return &Arrival{folder: f, transfer: transfer}
}

func (f *Folder) start(a *Arrival, bytes int64, entries int) error {
	if entries < 1 || entries > clipboard.FilesAndFoldersLimit || bytes < 0 || bytes > clipboard.FileCopyLimit {
		return fmt.Errorf("%w: %d files and folders, %d bytes", ErrOutsideTheLimits, entries, bytes)
	}
	f.mutex.Lock()
	defer f.mutex.Unlock()
	if err := os.MkdirAll(f.config.Directory, 0o700); err != nil {
		return fmt.Errorf("arrival: %w", err)
	}
	completed, _ := f.look()
	if err := f.makeRoom(completed, bytes); err != nil {
		return err
	}
	name, err := arrivalName(a.transfer)
	if err != nil {
		return err
	}
	path := filepath.Join(f.config.Directory, name)
	if err := os.Mkdir(path, 0o700); err != nil {
		return fmt.Errorf("arrival: %w", err)
	}
	a.name, a.path, a.bytes, a.entries, a.made = name, path, bytes, entries, make(map[string]made)
	f.writing[name] = a
	return nil
}

func (f *Folder) removeFinished(name string) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.remove(name)
}

func (f *Folder) RemoveOld() error {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	completed, failed := f.look()
	now := f.config.now()
	for _, arrival := range completed {
		if now.Sub(arrival.completed) >= KeptFor {
			failed = errors.Join(failed, f.remove(arrival.name))
		}
	}
	return failed
}

func (f *Folder) makeRoom(completed []completedArrival, bytes int64) error {
	free, err := f.config.freeSpace(f.config.Directory)
	if err != nil {
		return fmt.Errorf("arrival: reading the free space of the disk: %w", err)
	}
	var reserved, unwritten, kept int64
	for _, arrival := range f.writing {
		reserved += arrival.bytes
		unwritten += arrival.bytes - arrival.written.Load()
	}
	for _, arrival := range completed {
		kept += arrival.bytes
	}
	fits := func(kept, free int64) bool {
		return reserved+kept+bytes <= f.config.Room && free-unwritten-bytes >= f.config.Margin
	}
	if !fits(0, free+kept) {
		return fmt.Errorf("%w: %d bytes asked, %d bytes still to arrive, %d bytes free on the disk, a room of %d bytes and a margin of %d bytes",
			ErrNoRoom, bytes, unwritten, free, f.config.Room, f.config.Margin)
	}
	for !fits(kept, free) {
		oldest := completed[0]
		completed = completed[1:]
		if err := f.remove(oldest.name); err != nil {
			return err
		}
		kept -= oldest.bytes
		free += oldest.bytes
	}
	return nil
}

func (f *Folder) look() ([]completedArrival, error) {
	listed, err := os.ReadDir(f.config.Directory)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("arrival: %w", err)
	}
	folders := make(map[string]bool)
	var records []string
	for _, entry := range listed {
		switch name := entry.Name(); {
		case entry.IsDir():
			folders[name] = true
		case entry.Type().IsRegular() && strings.HasSuffix(name, recordSuffix):
			records = append(records, strings.TrimSuffix(name, recordSuffix))
		}
	}
	var failed error
	for _, name := range records {
		if !folders[name] && f.writing[name] == nil {
			failed = errors.Join(failed, removeIfThere(f.recordOf(name)))
		}
	}
	var completed []completedArrival
	for name := range folders {
		if f.writing[name] != nil {
			continue
		}
		if arrival, err := f.readRecord(name); err == nil {
			completed = append(completed, arrival)
			continue
		}
		failed = errors.Join(failed, f.remove(name))
	}
	slices.SortFunc(completed, func(a, b completedArrival) int {
		return cmp.Or(a.completed.Compare(b.completed), strings.Compare(a.name, b.name))
	})
	return completed, failed
}

func (f *Folder) recordOf(name string) string {
	return filepath.Join(f.config.Directory, name+recordSuffix)
}

func (f *Folder) writeRecord(name string, completed time.Time, bytes int64) error {
	record := f.recordOf(name)
	file, err := os.OpenFile(record, os.O_CREATE|os.O_EXCL|os.O_WRONLY|noFollow, 0o600)
	if err != nil {
		return fmt.Errorf("arrival: %w", err)
	}
	_, err = fmt.Fprintf(file, "%d %d\n", completed.UnixNano(), bytes)
	if err = errors.Join(err, file.Close()); err != nil {
		os.Remove(record)
		return fmt.Errorf("arrival: %w", err)
	}
	return nil
}

func (f *Folder) readRecord(name string) (completedArrival, error) {
	content, err := os.ReadFile(f.recordOf(name))
	if err != nil {
		return completedArrival{}, err
	}
	var nanoseconds, bytes int64
	if _, err := fmt.Sscanf(string(content), "%d %d", &nanoseconds, &bytes); err != nil || bytes < 0 {
		return completedArrival{}, errUnreadableRecord
	}
	return completedArrival{name: name, completed: time.Unix(0, nanoseconds), bytes: bytes}, nil
}

func (f *Folder) remove(name string) error {
	if err := removeIfThere(f.recordOf(name)); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(f.config.Directory, name)); err != nil {
		return fmt.Errorf("arrival: %w", err)
	}
	return nil
}

func (f *Folder) forget(name string) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	delete(f.writing, name)
}

func removeIfThere(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("arrival: %w", err)
	}
	return nil
}

func arrivalName(transfer int) (string, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("arrival: %w", err)
	}
	return fmt.Sprintf("%d-%x", transfer, random[:]), nil
}
