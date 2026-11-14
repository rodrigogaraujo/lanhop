package arrival

import (
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
)

func swapForALink(t *testing.T, folder, outside string) {
	t.Helper()
	if err := os.Rename(folder, folder+"-moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, folder); err != nil {
		t.Fatal(err)
	}
}

func TestAFolderSwappedForALinkAfterItArrivedIsNeverWrittenThrough(t *testing.T) {
	t.Run("between entries", func(t *testing.T) {
		f, _, _ := openFolder(t, 0, 0)
		outside := t.TempDir()
		docs, plan := aFolder("docs"), aFile("docs/plan.md", "plan")
		a := expecting(t, f, 1, docs, plan)
		sendAll(t, a, docs)
		swapForALink(t, filepath.Join(a.path, "docs"), outside)
		if _, err := a.File(plan.name, plan.size()); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("File(docs/plan.md) through a folder swapped for a link = %v, want ErrUnsafePath", err)
		}
		a.GiveUp()
		if listed, _ := os.ReadDir(outside); len(listed) != 0 {
			t.Errorf("the folder the link points to got %d entries, want none", len(listed))
		}
		expectGone(t, a)
	})
	t.Run("deeper down, between entries", func(t *testing.T) {
		f, _, _ := openFolder(t, 0, 0)
		outside := t.TempDir()
		entries := []sent{aFolder("docs"), aFolder("docs/2026"), aFolder("docs/2026/11")}
		a := expecting(t, f, 1, entries...)
		sendAll(t, a, entries[:2]...)
		swapForALink(t, filepath.Join(a.path, "docs"), outside)
		if err := os.MkdirAll(filepath.Join(outside, "2026"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := send(a, entries[2]); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("Folder(docs/2026/11) under a folder swapped for a link = %v, want ErrUnsafePath", err)
		}
		if _, err := os.Lstat(filepath.Join(outside, "2026", "11")); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("a folder was made through the link: %v", err)
		}
		expectGone(t, a)
	})
	t.Run("while a file is written", func(t *testing.T) {
		f, _, _ := openFolder(t, 0, 0)
		outside := t.TempDir()
		docs, plan := aFolder("docs"), aFile("docs/plan.md", "plan")
		a := expecting(t, f, 1, docs, plan)
		sendAll(t, a, docs)
		file, err := a.File(plan.name, plan.size())
		if err != nil {
			t.Fatal(err)
		}
		if err := writeInPieces(file, plan.content); err != nil {
			t.Fatal(err)
		}
		swapForALink(t, filepath.Join(a.path, "docs"), outside)
		if err := file.Whole(); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("Whole into a folder swapped for a link = %v, want ErrUnsafePath", err)
		}
		if listed, _ := os.ReadDir(outside); len(listed) != 0 {
			t.Errorf("the folder the link points to got %d entries, want none", len(listed))
		}
		expectGone(t, a)
	})
}

func TestAnArrivalFolderThatIsALinkIsRefused(t *testing.T) {
	real := t.TempDir()
	for _, inside := range []string{"not-an-arrival", "7-0123456789abcdef"} {
		if err := os.Mkdir(filepath.Join(real, inside), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(t.TempDir(), "arrived")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if f, err := Open(Config{Directory: link}); !errors.Is(err, ErrUnsafeDirectory) {
		t.Errorf("Open of a linked folder = %v, want ErrUnsafeDirectory", err)
		if f != nil {
			f.Close()
		}
	}
	listed, err := os.ReadDir(real)
	if err != nil || len(listed) != 2 {
		t.Errorf("the folder behind the link holds %d entries (%v), want both kept", len(listed), err)
	}
}

func TestAnArrivalFolderOthersCanWriteInIsRefused(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "arrived")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o770); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(Config{Directory: directory}); !errors.Is(err, ErrUnsafeDirectory) {
		t.Errorf("Open of a folder its group can write in = %v, want ErrUnsafeDirectory", err)
	}
}

func TestAnArrivalFolderOfAnotherUserIsRefused(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("giving a folder to another user needs root")
	}
	directory := filepath.Join(t.TempDir(), "arrived")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(directory, 4242, 4242); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(Config{Directory: directory}); !errors.Is(err, ErrUnsafeDirectory) {
		t.Errorf("Open of a folder of another user = %v, want ErrUnsafeDirectory", err)
	}
}

func TestCleaningUpOnlyTouchesTransferFoldersAndTheirRecords(t *testing.T) {
	f, clock, _ := openFolder(t, 0, 0)
	kept := []string{"not-an-arrival", "12-notahexnumber00", "12-0123456789ABCDEF", "photos.complete", "notes.txt", ".DS_Store"}
	for _, name := range kept {
		path := filepath.Join(f.Directory(), name)
		if filepath.Ext(name) != "" || name == ".DS_Store" {
			if err := os.WriteFile(path, []byte("not lanhop's"), 0o600); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Join(path, "inside"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	leftover := &Arrival{folder: f, name: "-3-00000000000000ee", path: filepath.Join(f.Directory(), "-3-00000000000000ee")}
	if err := os.Mkdir(leftover.path, 0o700); err != nil {
		t.Fatal(err)
	}

	clock.pass(48 * time.Hour)
	if err := f.RemoveOld(); err != nil {
		t.Fatalf("RemoveOld: %v", err)
	}
	expectGone(t, leftover)
	a := expectingWith(t, f, 1, 0, 1)
	expectHolds(t, f, append(kept, a.name)...)
}

func TestARecordWithASizeOverTheLimitMakesALeftover(t *testing.T) {
	for _, bytes := range []int64{clipboard.FileCopyLimit + 1, math.MaxInt64, -1} {
		f, clock, _ := openFolder(t, 0, 0)
		planted := plantCompleted(t, f, "5-00000000000000ff", clock.now(), bytes)
		if err := f.RemoveOld(); err != nil {
			t.Fatalf("RemoveOld: %v", err)
		}
		expectGone(t, planted)
	}
}

func TestARecordFinishedMoreThanADayInTheFutureCountsAsOld(t *testing.T) {
	f, clock, _ := openFolder(t, 0, 0)
	now := plantCompleted(t, f, "1-0000000000000001", clock.now(), 5)
	farAhead := plantCompleted(t, f, "2-0000000000000002", clock.now().Add(KeptFor+time.Hour), 5)
	if err := f.RemoveOld(); err != nil {
		t.Fatalf("RemoveOld: %v", err)
	}
	expectKept(t, now)
	expectGone(t, farAhead)
}

func TestAnArrivalJustFinishedIsKeptWhenTheClockStepsBack(t *testing.T) {
	f, clock, _ := openFolder(t, 0, 0)
	fresh, _ := arrive(t, f, 1, aFile("report.txt", "fresh"))
	clock.pass(-2 * time.Second)
	if err := f.RemoveOld(); err != nil {
		t.Fatalf("RemoveOld: %v", err)
	}
	expectKept(t, fresh)
}

func TestAFolderTakesItsLockAgainAfterTheCacheWasEmptied(t *testing.T) {
	f, _, _ := openFolder(t, 0, 0)
	if err := os.RemoveAll(f.Directory()); err != nil {
		t.Fatal(err)
	}
	if err := f.RemoveOld(); err != nil {
		t.Fatalf("RemoveOld after the cache was emptied: %v", err)
	}
	if second, err := Open(Config{Directory: f.Directory()}); !errors.Is(err, ErrFolderInUse) {
		if err == nil {
			second.Close()
		}
		t.Errorf("a second Open after the cache was emptied = %v, want ErrFolderInUse", err)
	}
}

func TestAFolderWhoseLockWasTakenByAnotherAfterTheCacheWasEmptiedStops(t *testing.T) {
	f, _, _ := openFolder(t, 0, 0)
	if err := os.RemoveAll(f.Directory()); err != nil {
		t.Fatal(err)
	}
	second, err := Open(Config{Directory: f.Directory()})
	if err != nil {
		t.Fatalf("Open after the cache was emptied: %v", err)
	}
	t.Cleanup(func() { second.Close() })
	if err := f.Expecting(1).Expect(5, 1); !errors.Is(err, ErrFolderInUse) {
		t.Errorf("Expect on the folder that lost its lock = %v, want ErrFolderInUse", err)
	}
}

func TestTheRoomArithmeticNeverOverflows(t *testing.T) {
	f, clock, disk := openFolder(t, math.MaxInt64, 1)
	disk.setFree(math.MaxInt64)
	planted := plantCompleted(t, f, "1-0000000000000001", clock.now(), clipboard.FileCopyLimit)
	expectingWith(t, f, 2, clipboard.FileCopyLimit, 1).GiveUp()
	expectKept(t, planted)

	f.config.Margin = math.MaxInt64
	if err := f.Expecting(3).Expect(1, 1); !errors.Is(err, ErrNoRoom) {
		t.Errorf("Expect with a margin of the largest number = %v, want ErrNoRoom", err)
	}
	expectKept(t, planted)
}

func TestASecondFolderOnTheSameDirectoryIsRefusedUntilTheFirstCloses(t *testing.T) {
	f, _, _ := openFolder(t, 0, 0)
	writing := expectingWith(t, f, 1, 0, 1)
	if second, err := Open(Config{Directory: f.Directory()}); !errors.Is(err, ErrFolderInUse) {
		t.Errorf("a second Open on the same folder = %v, want ErrFolderInUse", err)
		if second != nil {
			second.Close()
		}
	}
	if info, err := os.Lstat(writing.path); err != nil || !info.IsDir() {
		t.Errorf("the arrival in progress is gone after the second Open: %v", err)
	}

	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Errorf("a second Close: %v", err)
	}
	if err := f.Expecting(2).Expect(0, 1); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("Expect after Close = %v, want fs.ErrClosed", err)
	}
	if err := f.RemoveOld(); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("RemoveOld after Close = %v, want fs.ErrClosed", err)
	}
	next, err := Open(Config{Directory: f.Directory()})
	if err != nil {
		t.Fatalf("Open after Close: %v", err)
	}
	next.Close()
}

func TestAFileWhoseNameAppearsJustBeforeItIsPlacedIsNeverOverwritten(t *testing.T) {
	f, _, _ := openFolder(t, 0, 0)
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.config.beforePlacing = func(final string) {
		if err := os.Link(outside, final); err != nil {
			t.Errorf("putting a file at %s before it is placed: %v", final, err)
		}
	}
	plan := aFile("plan.md", "the plan that arrived")
	a := expecting(t, f, 1, plan)
	file, err := a.File(plan.name, plan.size())
	if err != nil {
		t.Fatal(err)
	}
	if err := writeInPieces(file, plan.content); err != nil {
		t.Fatal(err)
	}
	if err := file.Whole(); !errors.Is(err, ErrUnsafePath) {
		t.Errorf("Whole when the name appeared just before = %v, want ErrUnsafePath", err)
	}
	expectUntouched(t, outside, "untouched")
	expectGone(t, a)
}

func TestNamesThatOnlyDifferInCaseOrUnicodeFormAreKeptApartOrRefusedNeverOverwritten(t *testing.T) {
	for name, pair := range map[string][2]string{
		"case":         {"README", "readme"},
		"Unicode form": {"café.txt", "café.txt"},
	} {
		t.Run(name, func(t *testing.T) {
			f, _, _ := openFolder(t, 0, 0)
			first, second := aFile(pair[0], "first"), aFile(pair[1], "second")
			a := expecting(t, f, 1, first, second)
			sendAll(t, a, first)
			_, err := os.Lstat(filepath.Join(a.path, pair[1]))
			sameNameOnThisDisk := err == nil
			err = send(a, second)
			if sameNameOnThisDisk {
				if !errors.Is(err, ErrUnsafePath) {
					t.Errorf("sending %q after %q on a disk that sees one name = %v, want ErrUnsafePath", pair[1], pair[0], err)
				}
				expectGone(t, a)
				return
			}
			if err != nil {
				t.Fatalf("sending %q after %q on a disk that sees two names: %v", pair[1], pair[0], err)
			}
			expectContent(t, filepath.Join(a.path, pair[0]), "first")
			expectContent(t, filepath.Join(a.path, pair[1]), "second")
		})
	}
}
