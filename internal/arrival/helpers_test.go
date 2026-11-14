package arrival

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
)

type clock struct {
	mutex sync.Mutex
	at    time.Time
}

func (c *clock) now() time.Time {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.at
}

func (c *clock) pass(duration time.Duration) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.at = c.at.Add(duration)
}

type disk struct {
	mutex sync.Mutex
	free  int64
}

func (d *disk) freeSpace(string) (int64, error) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	return d.free, nil
}

func (d *disk) setFree(free int64) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	d.free = free
}

func openFolder(t *testing.T, room, margin int64) (*Folder, *clock, *disk) {
	t.Helper()
	ticking := &clock{at: time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)}
	plenty := &disk{free: 1 << 40}
	f, err := Open(Config{
		Directory: filepath.Join(t.TempDir(), "cache", "lanhop", "arrived"),
		Room:      room,
		Margin:    margin,
		now:       ticking.now,
		freeSpace: plenty.freeSpace,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	return f, ticking, plenty
}

type sent struct {
	name    string
	folder  bool
	content string
}

func aFile(name, content string) sent {
	return sent{name: name, content: content}
}

func aFolder(name string) sent {
	return sent{name: name, folder: true}
}

func (s sent) size() int64 {
	return int64(len(s.content))
}

func announced(entries []sent) (int64, int) {
	var total int64
	for _, entry := range entries {
		total += entry.size()
	}
	return total, len(entries)
}

func writeInPieces(file *IncomingFile, content string) error {
	const piece = 32 << 10
	for len(content) > 0 {
		next := min(piece, len(content))
		if _, err := file.Write([]byte(content[:next])); err != nil {
			return err
		}
		content = content[next:]
	}
	return nil
}

func send(a *Arrival, entry sent) error {
	if entry.folder {
		return a.Folder(entry.name)
	}
	file, err := a.File(entry.name, entry.size())
	if err != nil {
		return err
	}
	if err := writeInPieces(file, entry.content); err != nil {
		return err
	}
	return file.Whole()
}

func expectingWith(t *testing.T, f *Folder, transfer int, bytes int64, entries int) *Arrival {
	t.Helper()
	a := f.Expecting(transfer)
	if err := a.Expect(bytes, entries); err != nil {
		t.Fatalf("Expect(%d bytes, %d entries) for transfer %d: %v", bytes, entries, transfer, err)
	}
	return a
}

func expecting(t *testing.T, f *Folder, transfer int, entries ...sent) *Arrival {
	t.Helper()
	total, count := announced(entries)
	return expectingWith(t, f, transfer, total, count)
}

func sendAll(t *testing.T, a *Arrival, entries ...sent) {
	t.Helper()
	for _, entry := range entries {
		if err := send(a, entry); err != nil {
			t.Fatalf("sending %q: %v", entry.name, err)
		}
	}
}

func arrive(t *testing.T, f *Folder, transfer int, entries ...sent) (*Arrival, []string) {
	t.Helper()
	a := expecting(t, f, transfer, entries...)
	sendAll(t, a, entries...)
	if err := a.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	return a, a.Paths()
}

func plantCompleted(t *testing.T, f *Folder, name string, completed time.Time, bytes int64) *Arrival {
	t.Helper()
	path := filepath.Join(f.Directory(), name)
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := f.writeRecord(name, completed, bytes); err != nil {
		t.Fatal(err)
	}
	return &Arrival{folder: f, name: name, path: path}
}

func expectGone(t *testing.T, a *Arrival) {
	t.Helper()
	if _, err := os.Lstat(a.path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the folder of arrival %s is still there (%v), want it removed", a.name, err)
	}
	if _, err := os.Lstat(a.folder.recordOf(a.name)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the record of arrival %s is still there (%v), want it removed", a.name, err)
	}
}

func expectKept(t *testing.T, a *Arrival) {
	t.Helper()
	if info, err := os.Lstat(a.path); err != nil || !info.IsDir() {
		t.Errorf("the folder of arrival %s is gone (%v), want it kept", a.name, err)
	}
	if _, err := a.folder.readRecord(a.name); err != nil {
		t.Errorf("the record of arrival %s cannot be read (%v), want it kept", a.name, err)
	}
}

func expectHolds(t *testing.T, f *Folder, names ...string) {
	t.Helper()
	listed, err := os.ReadDir(f.Directory())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	var got []string
	for _, entry := range listed {
		if entry.Name() != lockName {
			got = append(got, entry.Name())
		}
	}
	slices.Sort(names)
	if !slices.Equal(got, names) {
		t.Errorf("the arrival folder holds %q, want %q", got, names)
	}
}

func expectContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Errorf("%s holds %d bytes (%v), want the %d bytes that were sent", path, len(got), err, len(want))
	}
}

func expectUntouched(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Errorf("%s outside the arrival folder holds %q (%v), want %q untouched", path, got, err, want)
	}
}

func expectMode(t *testing.T, path string, want fs.FileMode) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Errorf("%s: %v", path, err)
		return
	}
	if info.Mode() != want {
		t.Errorf("%s has mode %v, want %v", path, info.Mode(), want)
	}
}
