package filestream

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
)

func TestCountGivesTheFilesAndFoldersOfATreeAndTheirBytes(t *testing.T) {
	root := realTempDir(t)
	writeFile(t, filepath.Join(root, "report", "summary.txt"), "12345")
	writeFile(t, filepath.Join(root, "report", "charts", "march.svg"), "1234567890")
	if err := os.Mkdir(filepath.Join(root, "report", "empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	photo := writeFile(t, filepath.Join(root, "elsewhere", "photo.jpg"), "abc")

	listing, err := Count([]string{filepath.Join(root, "report"), photo}, time.Now().Add(patience))

	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	want := Listing{Copy: clipboard.FileCopy{FilesAndFolders: 6, Bytes: 18}}
	if listing != want {
		t.Errorf("Count = %+v, want %+v", listing, want)
	}
}

func TestCountSkipsLinksInsideFoldersAndNeverFollowsThem(t *testing.T) {
	root := realTempDir(t)
	secret := writeFile(t, filepath.Join(root, "secret", "id_ed25519"), "private key")
	writeFile(t, filepath.Join(root, "project", "main.go"), "package main")
	if err := os.Symlink(filepath.Dir(secret), filepath.Join(root, "project", "keys")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "project", "key")); err != nil {
		t.Fatal(err)
	}

	listing, err := Count([]string{filepath.Join(root, "project")}, time.Time{})

	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	want := Listing{Copy: clipboard.FileCopy{FilesAndFolders: 2, Bytes: int64(len("package main"))}, Skipped: 2}
	if listing != want {
		t.Errorf("Count = %+v, want %+v", listing, want)
	}
}

func TestACopyOfALinkIsNotSendable(t *testing.T) {
	root := realTempDir(t)
	target := writeFile(t, filepath.Join(root, "notes.txt"), "notes")
	link := filepath.Join(root, "link-to-notes")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	_, err := Count([]string{link}, time.Time{})

	if !errors.Is(err, ErrNotSendable) || !strings.Contains(err.Error(), link) {
		t.Errorf("Count of a link = %v, want ErrNotSendable naming %s", err, link)
	}
}

func TestCountRefusesPathsItCannotNameOnTheOtherMachine(t *testing.T) {
	root := realTempDir(t)
	first := writeFile(t, filepath.Join(root, "a", "notes.txt"), "a")
	second := writeFile(t, filepath.Join(root, "b", "notes.txt"), "b")
	for _, c := range []struct {
		paths []string
		want  error
	}{
		{[]string{"notes.txt"}, ErrNotAbsolute},
		{[]string{"/"}, ErrNoName},
		{[]string{first, second}, ErrSameName},
		{nil, clipboard.ErrNotFiles},
	} {
		t.Run(fmt.Sprint(c.paths), func(t *testing.T) {
			if _, err := Count(c.paths, time.Time{}); !errors.Is(err, c.want) {
				t.Errorf("Count(%q) = %v, want %v", c.paths, err, c.want)
			}
		})
	}
}

func TestCountTakesTheNameOfAPathAfterCleaningIt(t *testing.T) {
	root := realTempDir(t)
	writeFile(t, filepath.Join(root, "report", "summary.txt"), "12345")

	listing, err := Count([]string{filepath.Join(root, "report") + "/./"}, time.Time{})

	if err != nil || listing.Copy.FilesAndFolders != 2 {
		t.Errorf("Count of a path ending in /./ = %+v, %v, want the folder and its file", listing, err)
	}
}

func TestCountStopsWhenTheTimeOfAnAnswerIsOver(t *testing.T) {
	root := realTempDir(t)
	writeFile(t, filepath.Join(root, "report", "summary.txt"), "12345")

	_, err := Count([]string{filepath.Join(root, "report")}, time.Now().Add(-time.Millisecond))

	if !errors.Is(err, ErrTooSlowToList) {
		t.Errorf("Count after its deadline = %v, want ErrTooSlowToList", err)
	}
}

func TestCountRefusesMoreThan10000FilesAndFolders(t *testing.T) {
	folder := filepath.Join(realTempDir(t), "many")
	if err := os.Mkdir(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	for i := range clipboard.FilesAndFoldersLimit {
		if err := os.WriteFile(filepath.Join(folder, fmt.Sprintf("%05d", i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	_, err := Count([]string{folder}, time.Time{})

	if !errors.Is(err, clipboard.ErrTooManyFiles) {
		t.Errorf("Count of a folder and 10000 files = %v, want clipboard.ErrTooManyFiles", err)
	}
}

func TestCountRefusesMoreThan4GiBWithoutReadingAByte(t *testing.T) {
	root := realTempDir(t)
	var paths []string
	for _, name := range []string{"first.img", "second.img"} {
		path := filepath.Join(root, name)
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := file.Truncate(clipboard.FileCopyLimit/2 + 1); err != nil {
			t.Fatal(err)
		}
		file.Close()
		paths = append(paths, path)
	}

	_, err := Count(paths, time.Time{})

	if !errors.Is(err, clipboard.ErrFilesTooLarge) {
		t.Errorf("Count of two sparse files of 2 GiB and a byte = %v, want clipboard.ErrFilesTooLarge", err)
	}
}

func TestCountNamesAFileItCannotOpen(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root opens a file without permission")
	}
	root := realTempDir(t)
	locked := writeFile(t, filepath.Join(root, "report", "locked.txt"), "x")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}

	_, err := Count([]string{filepath.Join(root, "report")}, time.Time{})

	if !errors.Is(err, os.ErrPermission) || !strings.Contains(err.Error(), locked) {
		t.Errorf("Count of a folder holding a file without permission = %v, want a permission error naming %s", err, locked)
	}
}

func TestCountRefusesANameThatIsNotUTF8(t *testing.T) {
	root := realTempDir(t)
	folder := filepath.Join(root, "report")
	if err := os.Mkdir(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "caf\xe9.txt"), nil, 0o600); err != nil {
		t.Skipf("this file system refuses a name that is not UTF-8: %v", err)
	}

	_, err := Count([]string{folder}, time.Time{})

	if !errors.Is(err, ErrNameNotUTF8) {
		t.Errorf("Count of a name in Latin-1 = %v, want ErrNameNotUTF8", err)
	}
}

func TestCountResolvesTwoDotsThroughALinkAsTheSystemDoes(t *testing.T) {
	root := realTempDir(t)
	writeFile(t, filepath.Join(root, "data", "notes"), "notes behind the link")
	writeFile(t, filepath.Join(root, "notes"), "other notes")
	if err := os.Mkdir(filepath.Join(root, "data", "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "data", "x"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}

	listing, err := Count([]string{filepath.Join(root, "link") + "/../notes"}, time.Time{})

	if err != nil || listing.Copy.Bytes != int64(len("notes behind the link")) {
		t.Errorf("Count of link/../notes = %+v, %v, want the notes next to the target of the link", listing, err)
	}
}

func TestLinksInsideAFolderDoNotCountTowardTheLimitOfEntries(t *testing.T) {
	root := realTempDir(t)
	folder := filepath.Join(root, "project")
	writeFile(t, filepath.Join(folder, "main.go"), "package main")
	for i := range 5 {
		if err := os.Symlink(filepath.Join(folder, "main.go"), filepath.Join(folder, fmt.Sprintf("link%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	l := newLister(func() error { return nil })
	l.entriesLimit = 2

	err := l.list([]string{folder})

	if err != nil || l.listing.Skipped != 5 || l.listing.Copy.FilesAndFolders != 2 {
		t.Errorf("listing a folder with a file and 5 links under a limit of 2 = %+v, %v, want 2 entries and 5 skipped", l.listing, err)
	}
}

func TestTheListingRefusesAFullPathAtTheLimitThatSendingCouldNotOpen(t *testing.T) {
	root := realTempDir(t)
	short := writeFile(t, filepath.Join(root, "report", "a.txt"), "a")
	long := writeFile(t, filepath.Join(root, "report", "much-longer-name.txt"), "b")
	l := newLister(func() error { return nil })
	l.pathLimit = len(short) + 1

	err := l.list([]string{filepath.Join(root, "report")})

	if !errors.Is(err, ErrPathTooLong) {
		t.Errorf("listing a tree holding %s under a limit of %d bytes = %v, want ErrPathTooLong", long, l.pathLimit, err)
	}
}
