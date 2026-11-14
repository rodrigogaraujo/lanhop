package arrival

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestAWholeArrivalOfNestedFoldersAndFilesGivesItsTopLevelPaths(t *testing.T) {
	f, _, _ := openFolder(t, 0, 0)
	files := []sent{
		aFile("photos/beach.jpg", "sand and sea"),
		aFile("photos/2026/snow.jpg", strings.Repeat("snow ", 40_000)),
		aFile("photos/empty.txt", ""),
		aFile("notes.txt", "remember the milk"),
		aFile("docs/deep/plan.md", "a plan in folders that were not announced"),
	}
	entries := append([]sent{aFolder("photos"), aFolder("photos/2026")}, files...)
	a, paths := arrive(t, f, 7, entries...)

	want := []string{filepath.Join(a.path, "photos"), filepath.Join(a.path, "notes.txt"), filepath.Join(a.path, "docs")}
	if !slices.Equal(paths, want) {
		t.Errorf("Complete gave %q, want the top-level paths %q", paths, want)
	}
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			t.Errorf("%s is not absolute", path)
		}
	}
	for _, file := range files {
		path := filepath.Join(a.path, file.entry.Path)
		expectContent(t, path, file.content)
		expectMode(t, path, 0o600)
	}
	for _, folder := range []string{"", "photos", "photos/2026", "docs", "docs/deep"} {
		expectMode(t, filepath.Join(a.path, folder), fs.ModeDir|0o700)
	}
	filepath.WalkDir(a.path, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && strings.HasPrefix(entry.Name(), ".lanhop-arriving-") {
			t.Errorf("a temporary file is left: %s", path)
		}
		return err
	})
	expectKept(t, a)
}

func TestEachArrivalGetsAFolderOfItsOwnNamedByTheTransferAndARandomPart(t *testing.T) {
	f, _, _ := openFolder(t, 0, 0)
	first := startWith(t, f, 42, 0, 1)
	second := startWith(t, f, 42, 0, 1)
	named := regexp.MustCompile(`^42-[0-9a-f]{16}$`)
	for _, a := range []*Arrival{first, second} {
		if !named.MatchString(a.name) || filepath.Dir(a.path) != f.Directory() {
			t.Errorf("the arrival is in %s, want a folder named 42-<16 hex digits> in %s", a.path, f.Directory())
		}
	}
	if first.name == second.name {
		t.Errorf("two arrivals of transfer 42 share the folder %s", first.name)
	}
}

func TestEachUnsafePathIsRefusedAndLeavesNothing(t *testing.T) {
	for name, path := range map[string]string{
		"absolute":                "/etc/passwd",
		"dot dot":                 "..",
		"dot dot first":           "../outside.txt",
		"dot dot in the middle":   "a/../b",
		"dot first":               "./a",
		"a lone dot":              ".",
		"a dot in the middle":     "a/./b",
		"two slashes":             "a//b",
		"a slash at the end":      "a/",
		"empty":                   "",
		"a NUL byte":              "a\x00b",
		"a backslash":             `a\b`,
		"a backslash and dot dot": `..\outside.txt`,
	} {
		t.Run(name, func(t *testing.T) {
			f, _, _ := openFolder(t, 0, 0)
			good, unsafe := aFile("first.txt", "first"), aFile(path, "x")
			a := start(t, f, 1, good, unsafe)
			receiveAll(t, a, good)
			if err := a.Receive(unsafe.entry, unsafe.stream()); !errors.Is(err, ErrUnsafePath) {
				t.Errorf("Receive(%q) = %v, want ErrUnsafePath", path, err)
			}
			expectGone(t, a)
			expectHolds(t, f)
		})
	}
}

func TestAPathThroughAFileIsRefused(t *testing.T) {
	for name, through := range map[string]sent{
		"a file":   aFile("notes/inside.txt", "inside"),
		"a folder": aFolder("notes/inside"),
	} {
		t.Run(name, func(t *testing.T) {
			f, _, _ := openFolder(t, 0, 0)
			file := aFile("notes", "a file, not a folder")
			a := start(t, f, 1, file, through)
			receiveAll(t, a, file)
			if err := a.Receive(through.entry, through.stream()); !errors.Is(err, ErrUnsafePath) {
				t.Errorf("Receive(%q) under a file = %v, want ErrUnsafePath", through.entry.Path, err)
			}
			expectGone(t, a)
		})
	}
}

func TestTheSamePathTwiceIsRefusedUnlessItIsTheSameFolder(t *testing.T) {
	for name, test := range map[string]struct {
		first, again sent
		refused      bool
	}{
		"a file twice":            {aFile("a.txt", "one"), aFile("a.txt", "two"), true},
		"a folder over a file":    {aFile("a", "one"), aFolder("a"), true},
		"a file over a folder":    {aFolder("a"), aFile("a", "two"), true},
		"a folder twice":          {aFolder("a"), aFolder("a"), false},
		"a folder made for files": {aFile("a/b.txt", "b"), aFolder("a"), false},
	} {
		t.Run(name, func(t *testing.T) {
			f, _, _ := openFolder(t, 0, 0)
			a := start(t, f, 1, test.first, test.again)
			receiveAll(t, a, test.first)
			err := a.Receive(test.again.entry, test.again.stream())
			if test.refused {
				if !errors.Is(err, ErrUnsafePath) {
					t.Errorf("Receive(%q) again = %v, want ErrUnsafePath", test.again.entry.Path, err)
				}
				expectGone(t, a)
				return
			}
			if err != nil {
				t.Fatalf("Receive(%q) again = %v, want it taken as the folder this arrival made", test.again.entry.Path, err)
			}
			if _, err := a.Complete(); err != nil {
				t.Errorf("Complete: %v", err)
			}
		})
	}
}

func TestAFolderPlantedAsALinkInTheArrivalIsNeverReused(t *testing.T) {
	for name, entry := range map[string]sent{
		"as the folder announced":     aFolder("docs"),
		"as the folder of a file":     aFile("docs/plan.md", "plan"),
		"as a folder deeper in there": aFile("docs/2026/plan.md", "plan"),
	} {
		t.Run(name, func(t *testing.T) {
			f, _, _ := openFolder(t, 0, 0)
			outside := t.TempDir()
			a := start(t, f, 1, entry)
			if err := os.Symlink(outside, filepath.Join(a.path, "docs")); err != nil {
				t.Fatal(err)
			}
			if err := a.Receive(entry.entry, entry.stream()); !errors.Is(err, ErrUnsafePath) {
				t.Errorf("Receive(%q) through a planted link = %v, want ErrUnsafePath", entry.entry.Path, err)
			}
			if listed, _ := os.ReadDir(outside); len(listed) != 0 {
				t.Errorf("the folder the link points to got %d entries, want none", len(listed))
			}
			expectGone(t, a)
		})
	}
}

func TestAFileIsNeverOpenedThroughALinkPlantedAtItsTemporaryName(t *testing.T) {
	f, _, _ := openFolder(t, 0, 0)
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "missing.txt")
	plan, notes := aFile("plan.md", "plan"), aFile("docs/notes.txt", "notes")
	for _, test := range []struct {
		target   string
		entry    sent
		position int
		under    string
	}{
		{outside, plan, 1, ""},
		{missing, notes, 1, "docs"},
	} {
		a := start(t, f, 1, test.entry)
		if test.under != "" {
			if err := os.Mkdir(filepath.Join(a.path, test.under), 0o700); err != nil {
				t.Fatal(err)
			}
			a.made[test.under] = madeFolder
		}
		if err := os.Symlink(test.target, filepath.Join(a.path, test.under, temporaryName(test.position))); err != nil {
			t.Fatal(err)
		}
		if err := a.Receive(test.entry.entry, test.entry.stream()); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("Receive(%q) with a link at its temporary name = %v, want ErrUnsafePath", test.entry.entry.Path, err)
		}
		expectGone(t, a)
	}
	expectUntouched(t, outside, "untouched")
	if _, err := os.Lstat(missing); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the link was followed and created %s: %v", missing, err)
	}
}

func TestAFileIsNeverPutInPlaceOverALinkPlantedAtItsName(t *testing.T) {
	f, _, _ := openFolder(t, 0, 0)
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan := aFile("plan.md", "plan")
	a := start(t, f, 1, plan)
	if err := os.Symlink(outside, filepath.Join(a.path, "plan.md")); err != nil {
		t.Fatal(err)
	}
	if err := a.Receive(plan.entry, plan.stream()); !errors.Is(err, ErrUnsafePath) {
		t.Errorf("Receive over a planted link = %v, want ErrUnsafePath", err)
	}
	expectUntouched(t, outside, "untouched")
	expectGone(t, a)
}

func TestAKindOtherThanFileOrFolderIsRefused(t *testing.T) {
	for _, kind := range []Kind{0, 3, 4, 255} {
		f, _, _ := openFolder(t, 0, 0)
		a := startWith(t, f, 1, 0, 1)
		if err := a.Receive(Entry{Path: "link", Kind: kind}, strings.NewReader("")); !errors.Is(err, ErrNotFileOrFolder) {
			t.Errorf("Receive of kind %d = %v, want ErrNotFileOrFolder", kind, err)
		}
		expectGone(t, a)
	}
}

func TestASizeAnEntryCannotHaveIsRefused(t *testing.T) {
	for name, entry := range map[string]Entry{
		"a file with a negative size": {Path: "a.txt", Kind: FileKind, Size: -1},
		"a folder with a size":        {Path: "a", Kind: FolderKind, Size: 5},
	} {
		t.Run(name, func(t *testing.T) {
			f, _, _ := openFolder(t, 0, 0)
			a := startWith(t, f, 1, 5, 1)
			if err := a.Receive(entry, strings.NewReader("12345")); !errors.Is(err, ErrImpossibleSize) {
				t.Errorf("Receive(%+v) = %v, want ErrImpossibleSize", entry, err)
			}
			expectGone(t, a)
		})
	}
}

func TestMoreEntriesOrBytesThanAnnouncedAreRefused(t *testing.T) {
	for name, test := range map[string]struct {
		bytes   int64
		entries int
		sent    []sent
	}{
		"one entry more":                {5, 1, []sent{aFile("a.txt", "12345"), aFolder("b")}},
		"more bytes over two files":     {5, 2, []sent{aFile("a.txt", "123"), aFile("b.txt", "456")}},
		"a file larger than everything": {3, 1, []sent{aFile("a.txt", "12345")}},
	} {
		t.Run(name, func(t *testing.T) {
			f, _, _ := openFolder(t, 0, 0)
			a := startWith(t, f, 1, test.bytes, test.entries)
			last := len(test.sent) - 1
			receiveAll(t, a, test.sent[:last]...)
			if err := a.Receive(test.sent[last].entry, test.sent[last].stream()); !errors.Is(err, ErrMoreThanAnnounced) {
				t.Errorf("Receive(%q) = %v, want ErrMoreThanAnnounced", test.sent[last].entry.Path, err)
			}
			expectGone(t, a)
			expectHolds(t, f)
		})
	}
}

func TestAWrongDigestLeavesNothingBehind(t *testing.T) {
	f, _, _ := openFolder(t, 0, 0)
	first, report := aFile("first.txt", "first"), aFile("report.pdf", "the report")
	a := start(t, f, 1, first, report)
	receiveAll(t, a, first)
	if err := a.Receive(report.entry, report.streamWithDigestOf("another report")); !errors.Is(err, ErrDigestDiffers) {
		t.Errorf("Receive with a wrong digest = %v, want ErrDigestDiffers", err)
	}
	expectGone(t, a)
	expectHolds(t, f)
}

func TestAFileShorterThanItsSizeLeavesNothingBehind(t *testing.T) {
	f, _, _ := openFolder(t, 0, 0)
	first, report := aFile("first.txt", "first"), aFile("report.pdf", "the whole report")
	a := start(t, f, 1, first, report)
	receiveAll(t, a, first)
	if err := a.Receive(report.entry, strings.NewReader("the whole")); !errors.Is(err, ErrSizeDiffers) {
		t.Errorf("Receive of a file cut short = %v, want ErrSizeDiffers", err)
	}
	expectGone(t, a)
	expectHolds(t, f)
}

func TestAFileWithoutItsDigestLeavesNothingBehind(t *testing.T) {
	f, _, _ := openFolder(t, 0, 0)
	report := aFile("report.pdf", "the whole report")
	a := start(t, f, 1, report)
	if err := a.Receive(report.entry, strings.NewReader(report.content)); err == nil {
		t.Error("Receive of a file whose digest never came succeeded")
	}
	expectGone(t, a)
}

func TestAStopHalfwayLeavesNothingAndAnotherStopDoesNothing(t *testing.T) {
	f, _, _ := openFolder(t, 0, 0)
	entries := []sent{aFolder("photos"), aFile("photos/beach.jpg", "sand"), aFile("notes.txt", "milk")}
	a := start(t, f, 3, entries...)
	receiveAll(t, a, entries[:2]...)
	if err := a.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	expectGone(t, a)
	if err := a.Stop(); err != nil {
		t.Errorf("a second Stop: %v", err)
	}
	if err := a.Receive(entries[2].entry, entries[2].stream()); !errors.Is(err, ErrStopped) {
		t.Errorf("Receive after Stop = %v, want ErrStopped", err)
	}
	if _, err := a.Complete(); !errors.Is(err, ErrStopped) {
		t.Errorf("Complete after Stop = %v, want ErrStopped", err)
	}
	expectHolds(t, f)
}

func TestAStopWhileAFileIsBeingWrittenLeavesNothing(t *testing.T) {
	f, _, _ := openFolder(t, 0, 0)
	large := aFile("large.bin", strings.Repeat("x", 1<<20))
	a := start(t, f, 1, large)
	reader, writer := io.Pipe()
	received := make(chan error, 1)
	go func() { received <- a.Receive(large.entry, reader) }()
	if _, err := writer.Write([]byte(large.content[:256<<10])); err != nil {
		t.Fatal(err)
	}
	if err := a.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	writer.CloseWithError(errors.New("the data connection closed"))
	if err := <-received; err == nil {
		t.Error("Receive succeeded after the arrival stopped")
	}
	expectGone(t, a)
	expectHolds(t, f)
}

func TestAStopAfterCompleteKeepsTheFiles(t *testing.T) {
	f, _, _ := openFolder(t, 0, 0)
	notes := aFile("notes.txt", "milk")
	a, paths := arrive(t, f, 1, notes)
	if err := a.Stop(); err != nil {
		t.Fatalf("Stop after Complete: %v", err)
	}
	expectKept(t, a)
	expectContent(t, paths[0], notes.content)
	if again, err := a.Complete(); err != nil || !slices.Equal(again, paths) {
		t.Errorf("Complete again = %q, %v, want %q", again, err, paths)
	}
}

func TestCompletingBeforeEveryEntryArrivedIsRefusedAndLeavesNothing(t *testing.T) {
	f, _, _ := openFolder(t, 0, 0)
	entries := []sent{aFile("one.txt", "one"), aFile("two.txt", "two")}
	a := start(t, f, 1, entries...)
	receiveAll(t, a, entries[0])
	if _, err := a.Complete(); !errors.Is(err, ErrIncomplete) {
		t.Errorf("Complete with one of two entries = %v, want ErrIncomplete", err)
	}
	expectGone(t, a)
	expectHolds(t, f)
}

func TestAFileCopyOutsideTheLimitsIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	f, _, _ := openFolder(t, 0, 0)
	for _, refused := range []struct {
		bytes   int64
		entries int
	}{
		{0, 0},
		{0, -1},
		{0, 10_001},
		{-1, 1},
		{4<<30 + 1, 1},
	} {
		if _, err := f.Start(1, refused.bytes, refused.entries); !errors.Is(err, ErrOutsideTheLimits) {
			t.Errorf("Start with %d bytes and %d entries = %v, want ErrOutsideTheLimits", refused.bytes, refused.entries, err)
		}
	}
	if _, err := os.Lstat(f.Directory()); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the arrival folder was created for a refused copy: %v", err)
	}
	atTheLimits := startWith(t, f, 2, 4<<30, 10_000)
	if err := atTheLimits.Stop(); err != nil {
		t.Errorf("Stop: %v", err)
	}
}

func TestTheArrivalFolderComesBackWhenTheCacheWasEmptied(t *testing.T) {
	f, _, _ := openFolder(t, 0, 0)
	arrive(t, f, 1, aFile("first.txt", "first"))
	if err := os.RemoveAll(filepath.Dir(f.Directory())); err != nil {
		t.Fatal(err)
	}
	notes := aFile("notes.txt", "milk")
	_, paths := arrive(t, f, 2, notes)
	expectContent(t, paths[0], notes.content)
}

func TestTheQuarantineMarkSaysLanhopDownloadedItAtThatTime(t *testing.T) {
	if got := quarantineValue(time.Unix(0x6734a1c0, 0)); got != "0081;6734a1c0;lanhop;" {
		t.Errorf("quarantineValue = %q, want 0081;6734a1c0;lanhop;", got)
	}
}
