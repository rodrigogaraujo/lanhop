package arrival

import (
	"errors"
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
	entries := []sent{
		aFolder("photos"),
		aFile("photos/beach.jpg", "sand and sea"),
		aFolder("photos/2026"),
		aFile("photos/2026/snow.jpg", strings.Repeat("snow ", 40_000)),
		aFile("photos/empty.txt", ""),
		aFile("notes.txt", "remember the milk"),
		aFolder("docs"),
		aFolder("docs/deep"),
		aFile("docs/deep/plan.md", "a plan two folders down"),
	}
	a, paths := arrive(t, f, 7, entries...)

	want := []string{filepath.Join(a.path, "photos"), filepath.Join(a.path, "notes.txt"), filepath.Join(a.path, "docs")}
	if !slices.Equal(paths, want) {
		t.Errorf("Paths gave %q, want the top-level paths %q", paths, want)
	}
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			t.Errorf("%s is not absolute", path)
		}
	}
	for _, entry := range entries {
		path := filepath.Join(a.path, entry.name)
		if entry.folder {
			expectMode(t, path, fs.ModeDir|0o700)
			continue
		}
		expectContent(t, path, entry.content)
		expectMode(t, path, 0o600)
	}
	expectMode(t, a.path, fs.ModeDir|0o700)
	filepath.WalkDir(a.path, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && strings.HasPrefix(entry.Name(), ".lanhop-arriving-") {
			t.Errorf("a temporary file is left: %s", path)
		}
		return err
	})
	expectKept(t, a)
}

func TestPathsAreGivenOnlyOnceTheArrivalIsFinished(t *testing.T) {
	f, _, _ := openFolder(t, 0, 0)
	notes := aFile("notes.txt", "milk")
	a := expecting(t, f, 1, notes)
	sendAll(t, a, notes)
	if paths := a.Paths(); paths != nil {
		t.Errorf("Paths before Finish = %q, want none", paths)
	}
	if err := a.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if err := a.Finish(); err != nil {
		t.Errorf("a second Finish: %v", err)
	}
	if paths := a.Paths(); !slices.Equal(paths, []string{filepath.Join(a.path, "notes.txt")}) {
		t.Errorf("Paths after Finish = %q", paths)
	}
}

func TestEachArrivalGetsAFolderOfItsOwnNamedByTheTransferAndARandomPart(t *testing.T) {
	f, _, _ := openFolder(t, 0, 0)
	first := expectingWith(t, f, 42, 0, 1)
	second := expectingWith(t, f, 42, 0, 1)
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
		for kind, unsafe := range map[string]sent{"file": aFile(path, "x"), "folder": aFolder(path)} {
			t.Run(name+" as a "+kind, func(t *testing.T) {
				f, _, _ := openFolder(t, 0, 0)
				good := aFile("first.txt", "first")
				a := expecting(t, f, 1, good, unsafe)
				sendAll(t, a, good)
				if err := send(a, unsafe); !errors.Is(err, ErrUnsafePath) {
					t.Errorf("sending %q = %v, want ErrUnsafePath", path, err)
				}
				expectGone(t, a)
				expectHolds(t, f)
			})
		}
	}
}

func TestANameUnderAFileOrInAFolderThatDidNotArriveIsRefused(t *testing.T) {
	for name, test := range map[string]struct {
		before []sent
		next   sent
	}{
		"a file under a file":                      {[]sent{aFile("notes", "a file")}, aFile("notes/inside.txt", "inside")},
		"a folder under a file":                    {[]sent{aFile("notes", "a file")}, aFolder("notes/inside")},
		"a file in a folder that did not arrive":   {nil, aFile("docs/plan.md", "plan")},
		"a folder in a folder that did not arrive": {[]sent{aFolder("docs")}, aFolder("docs/2026/11")},
	} {
		t.Run(name, func(t *testing.T) {
			f, _, _ := openFolder(t, 0, 0)
			a := expecting(t, f, 1, append(test.before, test.next)...)
			sendAll(t, a, test.before...)
			if err := send(a, test.next); !errors.Is(err, ErrUnsafePath) {
				t.Errorf("sending %q = %v, want ErrUnsafePath", test.next.name, err)
			}
			expectGone(t, a)
		})
	}
}

func TestANameThatAlreadyExistsIsRefused(t *testing.T) {
	for name, test := range map[string]struct {
		first, again sent
	}{
		"a file twice":         {aFile("a.txt", "one"), aFile("a.txt", "two")},
		"a folder twice":       {aFolder("a"), aFolder("a")},
		"a folder over a file": {aFile("a", "one"), aFolder("a")},
		"a file over a folder": {aFolder("a"), aFile("a", "two")},
	} {
		t.Run(name, func(t *testing.T) {
			f, _, _ := openFolder(t, 0, 0)
			a := expecting(t, f, 1, test.first, test.again)
			sendAll(t, a, test.first)
			if err := send(a, test.again); !errors.Is(err, ErrUnsafePath) {
				t.Errorf("sending %q again = %v, want ErrUnsafePath", test.again.name, err)
			}
			expectGone(t, a)
		})
	}
	for _, kind := range []sent{aFile("plan.md", "plan"), aFolder("plan.md")} {
		t.Run("a plain file put there by something else, then a folder or a file", func(t *testing.T) {
			f, _, _ := openFolder(t, 0, 0)
			a := expecting(t, f, 1, kind)
			if err := os.WriteFile(filepath.Join(a.path, "plan.md"), []byte("put there by something else"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := send(a, kind); !errors.Is(err, ErrUnsafePath) {
				t.Errorf("sending %q over a file already there = %v, want ErrUnsafePath", kind.name, err)
			}
			expectGone(t, a)
		})
	}
}

func TestAFolderPlantedAsALinkInTheArrivalIsNeverReused(t *testing.T) {
	f, _, _ := openFolder(t, 0, 0)
	outside := t.TempDir()
	docs, plan := aFolder("docs"), aFile("docs/plan.md", "plan")
	a := expecting(t, f, 1, docs, plan)
	if err := os.Symlink(outside, filepath.Join(a.path, "docs")); err != nil {
		t.Fatal(err)
	}
	if err := send(a, docs); !errors.Is(err, ErrUnsafePath) {
		t.Errorf("Folder(docs) over a planted link = %v, want ErrUnsafePath", err)
	}
	if err := send(a, plan); !errors.Is(err, ErrGivenUp) {
		t.Errorf("File(docs/plan.md) after the refusal = %v, want ErrGivenUp", err)
	}
	if listed, _ := os.ReadDir(outside); len(listed) != 0 {
		t.Errorf("the folder the link points to got %d entries, want none", len(listed))
	}
	expectGone(t, a)
}

func TestAFileIsNeverOpenedThroughALinkPlantedAtItsTemporaryName(t *testing.T) {
	f, _, _ := openFolder(t, 0, 0)
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "missing.txt")
	for _, test := range []struct {
		target  string
		entries []sent
		under   string
	}{
		{outside, []sent{aFile("plan.md", "plan")}, ""},
		{missing, []sent{aFolder("docs"), aFile("docs/notes.txt", "notes")}, "docs"},
	} {
		a := expecting(t, f, 1, test.entries...)
		last := len(test.entries) - 1
		sendAll(t, a, test.entries[:last]...)
		if err := os.Symlink(test.target, filepath.Join(a.path, test.under, temporaryName(last+1))); err != nil {
			t.Fatal(err)
		}
		if err := send(a, test.entries[last]); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("sending %q with a link at its temporary name = %v, want ErrUnsafePath", test.entries[last].name, err)
		}
		expectGone(t, a)
	}
	expectUntouched(t, outside, "untouched")
	if _, err := os.Lstat(missing); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the link was followed and created %s: %v", missing, err)
	}
}

func TestAFileIsNeverPutInPlaceOverALinkPlantedAtItsName(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Run("planted before the file opens", func(t *testing.T) {
		f, _, _ := openFolder(t, 0, 0)
		plan := aFile("plan.md", "plan")
		a := expecting(t, f, 1, plan)
		if err := os.Symlink(outside, filepath.Join(a.path, "plan.md")); err != nil {
			t.Fatal(err)
		}
		if _, err := a.File(plan.name, plan.size()); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("File over a planted link = %v, want ErrUnsafePath", err)
		}
		expectGone(t, a)
	})
	t.Run("planted while the file is written", func(t *testing.T) {
		f, _, _ := openFolder(t, 0, 0)
		plan := aFile("plan.md", "plan")
		a := expecting(t, f, 1, plan)
		file, err := a.File(plan.name, plan.size())
		if err != nil {
			t.Fatal(err)
		}
		if err := writeInPieces(file, plan.content); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(a.path, "plan.md")); err != nil {
			t.Fatal(err)
		}
		if err := file.Whole(); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("Whole over a planted link = %v, want ErrUnsafePath", err)
		}
		expectGone(t, a)
	})
	expectUntouched(t, outside, "untouched")
}

func TestANegativeSizeIsRefused(t *testing.T) {
	f, _, _ := openFolder(t, 0, 0)
	a := expectingWith(t, f, 1, 5, 1)
	if _, err := a.File("a.txt", -1); !errors.Is(err, ErrImpossibleSize) {
		t.Errorf("File with -1 bytes = %v, want ErrImpossibleSize", err)
	}
	expectGone(t, a)
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
			a := expectingWith(t, f, 1, test.bytes, test.entries)
			last := len(test.sent) - 1
			sendAll(t, a, test.sent[:last]...)
			if err := send(a, test.sent[last]); !errors.Is(err, ErrMoreThanAnnounced) {
				t.Errorf("sending %q = %v, want ErrMoreThanAnnounced", test.sent[last].name, err)
			}
			expectGone(t, a)
			expectHolds(t, f)
		})
	}
}

func TestMoreBytesThanTheSizeOfAFileFailInWrite(t *testing.T) {
	f, _, _ := openFolder(t, 0, 0)
	a := expectingWith(t, f, 1, 10, 1)
	file, err := a.File("report.pdf", 5)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("123")); err != nil {
		t.Fatalf("Write within the size: %v", err)
	}
	if written, err := file.Write([]byte("456")); !errors.Is(err, ErrSizeDiffers) || written != 0 {
		t.Errorf("Write past the size = %d, %v, want 0 and ErrSizeDiffers", written, err)
	}
	expectGone(t, a)
}

func TestAFileShorterThanItsSizeIsNeverWhole(t *testing.T) {
	f, _, _ := openFolder(t, 0, 0)
	first := aFile("first.txt", "first")
	a := expectingWith(t, f, 1, first.size()+16, 2)
	sendAll(t, a, first)
	file, err := a.File("report.pdf", 16)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeInPieces(file, "the whole"); err != nil {
		t.Fatal(err)
	}
	if err := file.Whole(); !errors.Is(err, ErrSizeDiffers) {
		t.Errorf("Whole of a file cut short = %v, want ErrSizeDiffers", err)
	}
	expectGone(t, a)
	expectHolds(t, f)
}

func TestAFileThatNeverBecomesWholeLeavesNothingWhenTheArrivalIsGivenUp(t *testing.T) {
	f, _, _ := openFolder(t, 0, 0)
	first, report := aFile("first.txt", "first"), aFile("report.pdf", "the report, with a digest that does not match")
	a := expecting(t, f, 1, first, report)
	sendAll(t, a, first)
	file, err := a.File(report.name, report.size())
	if err != nil {
		t.Fatal(err)
	}
	if err := writeInPieces(file, report.content); err != nil {
		t.Fatal(err)
	}
	a.GiveUp()
	expectGone(t, a)
	expectHolds(t, f)
	if _, err := file.Write([]byte("x")); !errors.Is(err, ErrGivenUp) {
		t.Errorf("Write after GiveUp = %v, want ErrGivenUp", err)
	}
	if err := file.Whole(); !errors.Is(err, ErrGivenUp) {
		t.Errorf("Whole after GiveUp = %v, want ErrGivenUp", err)
	}
	if err := file.file.Close(); !errors.Is(err, os.ErrClosed) {
		t.Errorf("the open file was not closed by GiveUp: Close gave %v", err)
	}
}

func TestGivingUpHalfwayLeavesNothingAndAnotherGiveUpDoesNothing(t *testing.T) {
	f, _, _ := openFolder(t, 0, 0)
	entries := []sent{aFolder("photos"), aFile("photos/beach.jpg", "sand"), aFile("notes.txt", "milk")}
	a := expecting(t, f, 3, entries...)
	sendAll(t, a, entries[:2]...)
	a.GiveUp()
	expectGone(t, a)
	a.GiveUp()
	if err := send(a, entries[2]); !errors.Is(err, ErrGivenUp) {
		t.Errorf("sending after GiveUp = %v, want ErrGivenUp", err)
	}
	if err := a.Finish(); !errors.Is(err, ErrGivenUp) {
		t.Errorf("Finish after GiveUp = %v, want ErrGivenUp", err)
	}
	if paths := a.Paths(); paths != nil {
		t.Errorf("Paths after GiveUp = %q, want none", paths)
	}
	expectHolds(t, f)
}

func TestGivingUpFromAnotherGoroutineWhileAFileIsWrittenLeavesNothing(t *testing.T) {
	f, _, _ := openFolder(t, 0, 0)
	const size = 16 << 20
	a := expectingWith(t, f, 1, size, 1)
	file, err := a.File("large.bin", size)
	if err != nil {
		t.Fatal(err)
	}
	piece := make([]byte, 4<<10)
	written := make(chan error, 1)
	firstPiece := make(chan struct{})
	go func() {
		for sent := 0; ; sent += len(piece) {
			if _, err := file.Write(piece); err != nil {
				written <- err
				return
			}
			if sent == 0 {
				close(firstPiece)
			}
		}
	}()
	<-firstPiece
	a.GiveUp()
	if err := <-written; !errors.Is(err, ErrGivenUp) {
		t.Errorf("Write after GiveUp from another goroutine = %v, want ErrGivenUp", err)
	}
	expectGone(t, a)
	expectHolds(t, f)
}

func TestGivingUpAfterFinishRemovesEverything(t *testing.T) {
	f, _, _ := openFolder(t, 0, 0)
	a, paths := arrive(t, f, 1, aFolder("photos"), aFile("photos/beach.jpg", "sand"), aFile("notes.txt", "milk"))
	a.GiveUp()
	expectGone(t, a)
	for _, path := range paths {
		if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s is still there after GiveUp: %v", path, err)
		}
	}
	if paths := a.Paths(); paths != nil {
		t.Errorf("Paths after GiveUp = %q, want none", paths)
	}
	a.GiveUp()
	expectHolds(t, f)
}

func TestGivingUpBeforeExpectOrAfterARefusedExpectIsSafe(t *testing.T) {
	f, _, _ := openFolder(t, 100, 1)
	never := f.Expecting(1)
	never.GiveUp()
	never.GiveUp()
	if err := never.Expect(5, 1); !errors.Is(err, ErrGivenUp) {
		t.Errorf("Expect after GiveUp = %v, want ErrGivenUp", err)
	}
	refused := f.Expecting(2)
	if err := refused.Expect(101, 1); !errors.Is(err, ErrNoRoom) {
		t.Errorf("Expect of 101 bytes in a room of 100 = %v, want ErrNoRoom", err)
	}
	refused.GiveUp()
	expectHolds(t, f)
}

func TestEntriesOutOfOrderAreRefused(t *testing.T) {
	t.Run("an entry before Expect", func(t *testing.T) {
		f, _, _ := openFolder(t, 0, 0)
		a := f.Expecting(1)
		if err := a.Folder("photos"); !errors.Is(err, ErrOutOfOrder) {
			t.Errorf("Folder before Expect = %v, want ErrOutOfOrder", err)
		}
		expectHolds(t, f)
	})
	t.Run("Expect twice", func(t *testing.T) {
		f, _, _ := openFolder(t, 0, 0)
		a := expectingWith(t, f, 1, 0, 1)
		if err := a.Expect(0, 1); !errors.Is(err, ErrOutOfOrder) {
			t.Errorf("a second Expect = %v, want ErrOutOfOrder", err)
		}
		expectGone(t, a)
	})
	t.Run("an entry while a file is not whole", func(t *testing.T) {
		f, _, _ := openFolder(t, 0, 0)
		a := expectingWith(t, f, 1, 4, 2)
		if _, err := a.File("one.txt", 4); err != nil {
			t.Fatal(err)
		}
		if _, err := a.File("two.txt", 0); !errors.Is(err, ErrOutOfOrder) {
			t.Errorf("File while another file is open = %v, want ErrOutOfOrder", err)
		}
		expectGone(t, a)
	})
}

func TestFinishingBeforeEveryEntryIsWholeIsRefusedAndLeavesNothing(t *testing.T) {
	t.Run("an entry missing", func(t *testing.T) {
		f, _, _ := openFolder(t, 0, 0)
		entries := []sent{aFile("one.txt", "one"), aFile("two.txt", "two")}
		a := expecting(t, f, 1, entries...)
		sendAll(t, a, entries[0])
		if err := a.Finish(); !errors.Is(err, ErrIncomplete) {
			t.Errorf("Finish with one of two entries = %v, want ErrIncomplete", err)
		}
		expectGone(t, a)
		expectHolds(t, f)
	})
	t.Run("a file still open", func(t *testing.T) {
		f, _, _ := openFolder(t, 0, 0)
		a := expectingWith(t, f, 1, 3, 1)
		file, err := a.File("one.txt", 3)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeInPieces(file, "one"); err != nil {
			t.Fatal(err)
		}
		if err := a.Finish(); !errors.Is(err, ErrIncomplete) {
			t.Errorf("Finish before Whole = %v, want ErrIncomplete", err)
		}
		expectGone(t, a)
	})
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
		if err := f.Expecting(1).Expect(refused.bytes, refused.entries); !errors.Is(err, ErrOutsideTheLimits) {
			t.Errorf("Expect with %d bytes and %d entries = %v, want ErrOutsideTheLimits", refused.bytes, refused.entries, err)
		}
	}
	expectHolds(t, f)
	expectingWith(t, f, 2, 4<<30, 10_000).GiveUp()
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
