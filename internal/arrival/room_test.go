package arrival

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTheOldestArrivalsAreRemovedFirstToMakeRoom(t *testing.T) {
	f, clock, _ := openFolder(t, 100, 1)
	first, _ := arrive(t, f, 1, aFile("first.txt", strings.Repeat("1", 40)))
	clock.pass(time.Minute)
	second, _ := arrive(t, f, 2, aFile("second.txt", strings.Repeat("2", 40)))
	clock.pass(time.Minute)

	third := startWith(t, f, 3, 50, 1)
	expectGone(t, first)
	expectKept(t, second)

	fourth := startWith(t, f, 4, 50, 1)
	expectGone(t, second)
	expectHolds(t, f, third.name, fourth.name)
}

func TestAnArrivalInProgressIsNeverRemovedToMakeRoom(t *testing.T) {
	f, clock, _ := openFolder(t, 100, 1)
	half := aFile("half.bin", strings.Repeat("h", 60))
	writing := start(t, f, 1, half)
	clock.pass(time.Minute)
	done, _ := arrive(t, f, 2, aFile("done.txt", strings.Repeat("d", 30)))
	clock.pass(time.Minute)

	third := startWith(t, f, 3, 30, 1)
	expectGone(t, done)
	if _, err := f.Start(4, 20, 1); !errors.Is(err, ErrNoRoom) {
		t.Errorf("Start with only arrivals in progress to remove = %v, want ErrNoRoom", err)
	}
	expectHolds(t, f, writing.name, third.name)

	receiveAll(t, writing, half)
	if _, err := writing.Complete(); err != nil {
		t.Errorf("the arrival in progress cannot complete: %v", err)
	}
}

func TestAnArrivalThatCannotFitIsRefusedAndRemovesNothing(t *testing.T) {
	f, clock, _ := openFolder(t, 100, 1)
	first, _ := arrive(t, f, 1, aFile("first.txt", strings.Repeat("1", 40)))
	clock.pass(time.Minute)
	second, _ := arrive(t, f, 2, aFile("second.txt", strings.Repeat("2", 40)))

	if _, err := f.Start(3, 101, 1); !errors.Is(err, ErrNoRoom) {
		t.Errorf("Start of 101 bytes in a room of 100 = %v, want ErrNoRoom", err)
	}
	expectHolds(t, f, first.name, first.name+recordSuffix, second.name, second.name+recordSuffix)
}

func TestTheFreeSpaceOfTheDiskKeepsItsMargin(t *testing.T) {
	f, clock, disk := openFolder(t, 1<<30, 1000)
	old, _ := arrive(t, f, 1, aFile("old.txt", strings.Repeat("o", 40)))
	clock.pass(time.Minute)
	disk.setFree(1000 + 50)

	if _, err := f.Start(2, 200, 1); !errors.Is(err, ErrNoRoom) {
		t.Errorf("Start of more than the free space above the margin = %v, want ErrNoRoom", err)
	}
	expectKept(t, old)

	writing := startWith(t, f, 3, 60, 1)
	expectGone(t, old)

	disk.setFree(1000 + 100)
	if _, err := f.Start(4, 50, 1); !errors.Is(err, ErrNoRoom) {
		t.Errorf("Start while 60 bytes are still to arrive = %v, want ErrNoRoom", err)
	}
	expectHolds(t, f, writing.name)
}

func TestTheRoomIs8GiBAndTheMarginIs256MiBUnlessConfigured(t *testing.T) {
	f, clock, disk := openFolder(t, 0, 0)
	if f.config.Room != 8<<30 || f.config.Margin != 256<<20 {
		t.Fatalf("the room is %d bytes and the margin %d bytes, want 8 GiB and 256 MiB", f.config.Room, f.config.Margin)
	}
	var planted []*Arrival
	for i := range 3 {
		planted = append(planted, plantCompleted(t, f, fmt.Sprintf("%d-planted", i), clock.now(), 3<<30))
		clock.pass(time.Hour)
	}

	large := startWith(t, f, 9, 3<<30, 1)
	expectGone(t, planted[0])
	expectGone(t, planted[1])
	expectKept(t, planted[2])
	if err := large.Stop(); err != nil {
		t.Fatal(err)
	}

	disk.setFree(DefaultMargin + 100)
	small := startWith(t, f, 10, 100, 1)
	expectKept(t, planted[2])
	if err := small.Stop(); err != nil {
		t.Fatal(err)
	}
	startWith(t, f, 11, 101, 1)
	expectGone(t, planted[2])
}

func TestArrivalsAreRemovedADayAfterTheyCompleted(t *testing.T) {
	f, clock, _ := openFolder(t, 0, 0)
	first, _ := arrive(t, f, 1, aFile("first.txt", "first"))
	clock.pass(2 * time.Hour)
	second, _ := arrive(t, f, 2, aFile("second.txt", "second"))

	clock.pass(22*time.Hour - time.Nanosecond)
	if err := f.RemoveOld(); err != nil {
		t.Fatalf("RemoveOld: %v", err)
	}
	expectKept(t, first)
	expectKept(t, second)

	clock.pass(time.Nanosecond)
	if err := f.RemoveOld(); err != nil {
		t.Fatalf("RemoveOld: %v", err)
	}
	expectGone(t, first)
	expectKept(t, second)

	clock.pass(2 * time.Hour)
	if err := f.RemoveOld(); err != nil {
		t.Fatalf("RemoveOld: %v", err)
	}
	expectGone(t, second)
	expectHolds(t, f)
}

func leftBehindByACrash(t *testing.T, f *Folder) []*Arrival {
	t.Helper()
	writingWhenItCrashed := &Arrival{folder: f, name: "21-00000000000000aa", path: filepath.Join(f.Directory(), "21-00000000000000aa")}
	wholeButNotRecorded := &Arrival{folder: f, name: "22-00000000000000bb", path: filepath.Join(f.Directory(), "22-00000000000000bb")}
	recordLeftAlone := &Arrival{folder: f, name: "23-00000000000000cc", path: filepath.Join(f.Directory(), "23-00000000000000cc")}
	unreadableRecord := &Arrival{folder: f, name: "24-00000000000000dd", path: filepath.Join(f.Directory(), "24-00000000000000dd")}
	for path, content := range map[string]string{
		filepath.Join(writingWhenItCrashed.path, "photos", temporaryName(2)): "half a photo",
		filepath.Join(wholeButNotRecorded.path, "notes.txt"):                 "milk",
		filepath.Join(unreadableRecord.path, "notes.txt"):                    "milk",
		f.recordOf(unreadableRecord.name):                                    "not a record",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.writeRecord(recordLeftAlone.name, time.Now(), 5); err != nil {
		t.Fatal(err)
	}
	return []*Arrival{writingWhenItCrashed, wholeButNotRecorded, recordLeftAlone, unreadableRecord}
}

func TestCleaningUpKeepsAnArrivalInProgressAndRemovesWhatACrashLeftBehind(t *testing.T) {
	f, clock, _ := openFolder(t, 0, 0)
	one, two := aFile("one.txt", "one"), aFile("two.txt", "two")
	writing := start(t, f, 1, one, two)
	receiveAll(t, writing, one)
	crashed := leftBehindByACrash(t, f)

	clock.pass(48 * time.Hour)
	if err := f.RemoveOld(); err != nil {
		t.Fatalf("RemoveOld: %v", err)
	}
	for _, leftover := range crashed {
		expectGone(t, leftover)
	}
	expectHolds(t, f, writing.name)

	receiveAll(t, writing, two)
	paths, err := writing.Complete()
	if err != nil {
		t.Fatalf("the arrival in progress cannot complete after the clean-up: %v", err)
	}
	expectContent(t, paths[0], one.content)
	expectContent(t, paths[1], two.content)
}

func TestStartingAnArrivalRemovesWhatACrashLeftBehind(t *testing.T) {
	f, _, _ := openFolder(t, 0, 0)
	if err := os.MkdirAll(f.Directory(), 0o700); err != nil {
		t.Fatal(err)
	}
	crashed := leftBehindByACrash(t, f)
	a := startWith(t, f, 1, 0, 1)
	for _, leftover := range crashed {
		expectGone(t, leftover)
	}
	expectHolds(t, f, a.name)
}

func TestSeveralArrivalsAtOnceKeepTheirFilesApart(t *testing.T) {
	f, _, _ := openFolder(t, 0, 0)
	const machines = 4
	sentBy := func(machine int) []sent {
		return []sent{
			aFolder("desk"),
			aFile("desk/a.txt", fmt.Sprintf("machine %d, file a", machine)),
			aFile("desk/deeper/b.bin", strings.Repeat(fmt.Sprint(machine), 100_000)),
			aFile(fmt.Sprintf("from-%d.txt", machine), "top"),
		}
	}
	tidying := make(chan struct{})
	var tidied sync.WaitGroup
	tidied.Add(2)
	go func() {
		defer tidied.Done()
		for {
			select {
			case <-tidying:
				return
			default:
				f.RemoveOld()
			}
		}
	}()
	go func() {
		defer tidied.Done()
		for transfer := 100; ; transfer++ {
			select {
			case <-tidying:
				return
			default:
			}
			stopped, err := f.Start(transfer, 3, 1)
			if err != nil {
				t.Errorf("Start of an arrival that stops: %v", err)
				return
			}
			stopped.Receive(aFile("stopped.txt", "abc").entry, aFile("stopped.txt", "abc").stream())
			stopped.Stop()
		}
	}()

	paths := make([][]string, machines)
	arrivals := make([]*Arrival, machines)
	var arrived sync.WaitGroup
	for machine := range machines {
		arrived.Add(1)
		go func() {
			defer arrived.Done()
			entries := sentBy(machine)
			total, count := announced(entries)
			a, err := f.Start(machine, total, count)
			if err != nil {
				t.Errorf("Start for machine %d: %v", machine, err)
				return
			}
			for _, entry := range entries {
				if err := a.Receive(entry.entry, entry.stream()); err != nil {
					t.Errorf("Receive(%q) for machine %d: %v", entry.entry.Path, machine, err)
					return
				}
			}
			arrivals[machine] = a
			paths[machine], err = a.Complete()
			if err != nil {
				t.Errorf("Complete for machine %d: %v", machine, err)
			}
		}()
	}
	arrived.Wait()
	close(tidying)
	tidied.Wait()

	for machine, a := range arrivals {
		if a == nil || len(paths[machine]) != 2 {
			t.Fatalf("machine %d gave the paths %q", machine, paths[machine])
		}
		for _, entry := range sentBy(machine) {
			if entry.entry.Kind == FileKind {
				expectContent(t, filepath.Join(a.path, entry.entry.Path), entry.content)
			}
		}
	}
}

func TestArrivalsStartedAtOnceNeverTakeMoreThanTheRoom(t *testing.T) {
	f, _, _ := openFolder(t, 100, 1)
	const tries = 8
	var started, refused atomic.Int32
	var everyone sync.WaitGroup
	for transfer := range tries {
		everyone.Add(1)
		go func() {
			defer everyone.Done()
			_, err := f.Start(transfer, 40, 1)
			switch {
			case err == nil:
				started.Add(1)
			case errors.Is(err, ErrNoRoom):
				refused.Add(1)
			default:
				t.Errorf("Start: %v", err)
			}
		}()
	}
	everyone.Wait()
	if started.Load() != 2 || refused.Load() != tries-2 {
		t.Errorf("%d arrivals of 40 bytes started in a room of 100 and %d were refused, want 2 and %d", started.Load(), refused.Load(), tries-2)
	}
}

func TestMakingRoomAcrossGoroutinesNeverRemovesAnArrivalInProgress(t *testing.T) {
	file := aFile("round.txt", strings.Repeat("r", 1000))
	f, _, _ := openFolder(t, 3*file.entry.Size, 1)
	const machines, rounds = 6, 10
	var completed atomic.Int32
	var everyone sync.WaitGroup
	for machine := range machines {
		everyone.Add(1)
		go func() {
			defer everyone.Done()
			for round := 0; round < rounds; {
				a, err := f.Start(machine*rounds+round, file.entry.Size, 1)
				if errors.Is(err, ErrNoRoom) {
					time.Sleep(time.Millisecond)
					continue
				}
				if err != nil {
					t.Errorf("Start: %v", err)
					return
				}
				if err := a.Receive(file.entry, file.stream()); err != nil {
					t.Errorf("an arrival in progress lost its folder: %v", err)
					return
				}
				if _, err := a.Complete(); err != nil {
					t.Errorf("Complete: %v", err)
					return
				}
				completed.Add(1)
				round++
			}
		}()
	}
	everyone.Wait()
	if completed.Load() != machines*rounds {
		t.Errorf("%d arrivals completed, want %d", completed.Load(), machines*rounds)
	}
	listed, err := os.ReadDir(f.Directory())
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) > 3*2 {
		t.Errorf("the arrival folder holds %d entries after the room of 3 arrivals was made again and again, want at most 3 arrivals and their records", len(listed))
	}
}

func TestTheFreeSpaceIsReadFromTheDisk(t *testing.T) {
	free, err := freeSpaceOf(t.TempDir())
	if err != nil || free <= 0 {
		t.Errorf("freeSpaceOf = %d, %v, want the free bytes of the disk", free, err)
	}
}

func TestTheArrivalFolderLivesInTheCacheOfThePerson(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", "")
	want := filepath.Join(home, ".cache", "lanhop", "arrivals")
	if runtime.GOOS == "darwin" {
		want = filepath.Join(home, "Library", "Caches", "lanhop", "arrivals")
	}
	f, err := Open(Config{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if f.Directory() != want {
		t.Errorf("the arrival folder is %s, want %s", f.Directory(), want)
	}
	if runtime.GOOS != "linux" {
		return
	}
	elsewhere := filepath.Join(home, "elsewhere")
	t.Setenv("XDG_CACHE_HOME", elsewhere)
	f, err = Open(Config{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if want := filepath.Join(elsewhere, "lanhop", "arrivals"); f.Directory() != want {
		t.Errorf("with XDG_CACHE_HOME set, the arrival folder is %s, want %s", f.Directory(), want)
	}
}
