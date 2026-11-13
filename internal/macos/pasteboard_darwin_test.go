//go:build darwin && cgo

package macos

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
)

func pasteboardOfThisTest(t *testing.T) Pasteboard {
	t.Helper()
	p := Pasteboard{name: fmt.Sprintf("com.github.rodrigogaraujo.lanhop.test.%d", time.Now().UnixNano())}
	t.Cleanup(p.release)
	return p
}

func TestTextWrittenToThePasteboardReadsBackTheSame(t *testing.T) {
	texts := map[string]string{
		"a word":                 "hello",
		"accents, emoji, kanji":  "olá, ação 🙂👍🏽 日本語",
		"tabs and several lines": "line one\r\nline two\n\tindented\n",
		"an empty text":          "",
		"64 KiB":                 strings.Repeat("x", 64<<10),
		"the limit":              strings.Repeat("y", clipboard.Limit),
	}
	for name, text := range texts {
		t.Run(name, func(t *testing.T) {
			p := pasteboardOfThisTest(t)
			if _, err := p.Write(text); err != nil {
				t.Fatalf("Write: %v", err)
			}

			got, err := p.Read()

			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if got != text {
				t.Errorf("read %d bytes back, want the same %d bytes", len(got), len(text))
			}
		})
	}
}

func TestEachWriteMovesTheMarkAndReadingDoesNot(t *testing.T) {
	p := pasteboardOfThisTest(t)
	before := p.Mark()

	first, err := p.Write("first")
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if first == before {
		t.Errorf("the mark stayed at %d after a write", before)
	}
	if got := p.Mark(); got != first {
		t.Errorf("Mark = %d, want the %d that Write returned", got, first)
	}
	if _, err := p.Read(); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got := p.Mark(); got != first {
		t.Errorf("Mark after a read = %d, want %d", got, first)
	}

	second, err := p.Write("second")
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if second == first {
		t.Errorf("the mark stayed at %d after a second write", first)
	}
}

func TestAPasteboardWithNothingOnItIsEmpty(t *testing.T) {
	p := pasteboardOfThisTest(t)

	if _, err := p.Read(); !errors.Is(err, clipboard.ErrEmpty) {
		t.Errorf("Read of a pasteboard with nothing on it: error = %v, want %v", err, clipboard.ErrEmpty)
	}
}

func TestACopyWithoutTextIsNotText(t *testing.T) {
	p := pasteboardOfThisTest(t)
	if err := p.writeTyped("public.png", "\x89PNG\r\n\x1a\n", nil); err != nil {
		t.Fatalf("writeTyped: %v", err)
	}

	if _, err := p.Read(); !errors.Is(err, clipboard.ErrNotText) {
		t.Errorf("Read of an image: error = %v, want %v", err, clipboard.ErrNotText)
	}
}

func TestACopyThatAPasswordManagerMarkedPrivateIsNotRead(t *testing.T) {
	p := pasteboardOfThisTest(t)
	if _, err := p.write("a password", true); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := p.Read()

	if !errors.Is(err, clipboard.ErrPrivate) || got != "" {
		t.Errorf("Read = %q, %v, want nothing and %v", got, err, clipboard.ErrPrivate)
	}
}

func TestAnEmptiedPasteboardIsEmptyRatherThanNotText(t *testing.T) {
	p := pasteboardOfThisTest(t)
	if _, err := p.Write("a password"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	p.empty()

	if _, err := p.Read(); !errors.Is(err, clipboard.ErrEmpty) {
		t.Errorf("Read after the pasteboard was emptied: error = %v, want %v", err, clipboard.ErrEmpty)
	}
}

func TestACopyWithFormattingAndPlainTextReadsAsThePlainText(t *testing.T) {
	p := pasteboardOfThisTest(t)
	const rtf = `{\rtf1\ansi{\fonttbl\f0 Helvetica;}\f0\b bold\b0  and plain}`
	plain := "bold and plain"
	if err := p.writeTyped("public.rtf", rtf, &plain); err != nil {
		t.Fatalf("writeTyped: %v", err)
	}

	got, err := p.Read()

	if err != nil || got != plain {
		t.Errorf("Read = %q, %v, want the plain text", got, err)
	}
}

func TestACopyOfFilesIsNotTextEvenWithTheirNamesAsText(t *testing.T) {
	p := pasteboardOfThisTest(t)
	name := "report.pdf"
	if err := p.writeTyped("public.file-url", "file:///tmp/report.pdf", &name); err != nil {
		t.Fatalf("writeTyped: %v", err)
	}

	if got, err := p.Read(); !errors.Is(err, clipboard.ErrNotText) || got != "" {
		t.Errorf("Read = %q, %v, want nothing and %v", got, err, clipboard.ErrNotText)
	}
}

func TestATextOverTheLimitIsNotRead(t *testing.T) {
	p := pasteboardOfThisTest(t)
	if _, err := p.Write(strings.Repeat("z", clipboard.Limit+1)); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if _, err := p.Read(); !errors.Is(err, clipboard.ErrTooLarge) {
		t.Errorf("Read: error = %v, want %v", err, clipboard.ErrTooLarge)
	}
}

func TestWritingAndReading64KiBTakesLittleOfTheTimeACopyMayTake(t *testing.T) {
	const budget = 300 * time.Millisecond
	p := pasteboardOfThisTest(t)
	text := strings.Repeat("w", 64<<10)

	start := time.Now()
	if _, err := p.Write(text); err != nil {
		t.Fatalf("Write: %v", err)
	}
	wrote := time.Since(start)
	if _, err := p.Read(); err != nil {
		t.Fatalf("Read: %v", err)
	}
	both := time.Since(start)

	t.Logf("64 KiB: write %v, read %v", wrote, both-wrote)
	if both > budget/3 {
		t.Errorf("writing and reading 64 KiB took %v, more than a third of the %v that SC-001 allows a whole copy", both, budget)
	}
}

func TestTheGeneralPasteboardGivesAMarkWithoutReadingIt(t *testing.T) {
	if mark := GeneralPasteboard().Mark(); mark < 0 {
		t.Errorf("Mark of the general pasteboard = %d", mark)
	}
}

func TestFilesWrittenToThePasteboardReadBackAsTheSamePathsInOrder(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "a folder")
	if err := os.Mkdir(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	paths := []string{
		filepath.Join(dir, "a name with spaces.txt"),
		filepath.Join(dir, "olá, ação.pdf"),
		filepath.Join(dir, "decomposed ação.pdf"),
		filepath.Join(dir, "🙂👍🏽 日本語.png"),
		filepath.Join(dir, "50% off.txt"),
		filepath.Join(dir, "not%20a space.txt"),
		filepath.Join(dir, "#1 report.txt"),
		folder,
	}
	p := pasteboardOfThisTest(t)
	if _, err := p.WriteFiles(paths); err != nil {
		t.Fatalf("WriteFiles: %v", err)
	}

	got, err := p.ReadFiles()

	if err != nil {
		t.Fatalf("ReadFiles: %v", err)
	}
	if !slices.Equal(got, paths) {
		t.Errorf("ReadFiles = %+q, want %+q", got, paths)
	}
}

func TestAFileReferenceURLReadsBackAsThePathOfTheFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "a referenced file.txt")
	if err := os.WriteFile(file, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	reference, ok := fileReferenceURL(file)
	if !ok || !strings.HasPrefix(reference, "file:///.file/id=") {
		t.Fatalf("file reference URL of %s = %q, want one that starts with file:///.file/id=", file, reference)
	}
	p := pasteboardOfThisTest(t)
	if err := p.writeTyped("public.file-url", reference, nil); err != nil {
		t.Fatalf("writeTyped: %v", err)
	}

	got, err := p.ReadFiles()

	if err != nil {
		t.Fatalf("ReadFiles: %v", err)
	}
	if len(got) != 1 || strings.HasPrefix(got[0], "/.file/") || !sameFile(got[0], file) {
		t.Errorf("ReadFiles = %q, want the path of %s", got, file)
	}
}

func TestAFileReferenceURLToAFileThatIsGoneIsNotFiles(t *testing.T) {
	file := filepath.Join(t.TempDir(), "a removed file.txt")
	if err := os.WriteFile(file, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	reference, ok := fileReferenceURL(file)
	if !ok {
		t.Fatalf("no file reference URL for %s", file)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	p := pasteboardOfThisTest(t)
	if err := p.writeTyped("public.file-url", reference, nil); err != nil {
		t.Fatalf("writeTyped: %v", err)
	}

	got, err := p.ReadFiles()

	if !errors.Is(err, clipboard.ErrNotFiles) || got != nil {
		t.Errorf("ReadFiles = %q, %v, want nothing and %v", got, err, clipboard.ErrNotFiles)
	}
}

func TestACopyThatIsNotAListOfLocalFilesIsNotFiles(t *testing.T) {
	copies := map[string]func(Pasteboard) error{
		"a text copy that names a file": func(p Pasteboard) error {
			_, err := p.Write("file:///tmp/report.pdf")
			return err
		},
		"a web address copied as a URL": func(p Pasteboard) error {
			address := "https://example.com/report.pdf"
			return p.writeTyped("public.url", address, &address)
		},
		"an empty pasteboard": func(Pasteboard) error {
			return nil
		},
	}
	for name, copyOnto := range copies {
		t.Run(name, func(t *testing.T) {
			p := pasteboardOfThisTest(t)
			if err := copyOnto(p); err != nil {
				t.Fatalf("copy: %v", err)
			}

			got, err := p.ReadFiles()

			if !errors.Is(err, clipboard.ErrNotFiles) || got != nil {
				t.Errorf("ReadFiles = %q, %v, want nothing and %v", got, err, clipboard.ErrNotFiles)
			}
		})
	}
}

func TestACopyOfFilesWithTheirNamesAsTextReadsAsTheFiles(t *testing.T) {
	p := pasteboardOfThisTest(t)
	name := "report.pdf"
	if err := p.writeTyped("public.file-url", "file:///tmp/report.pdf", &name); err != nil {
		t.Fatalf("writeTyped: %v", err)
	}

	got, err := p.ReadFiles()

	if err != nil || !slices.Equal(got, []string{"/tmp/report.pdf"}) {
		t.Errorf("ReadFiles = %q, %v, want the file and no error", got, err)
	}
}

func TestFilesThatAPasswordManagerMarkedPrivateAreNotRead(t *testing.T) {
	p := pasteboardOfThisTest(t)
	if _, err := p.writeFiles([]string{"/tmp/secret.txt"}, true); err != nil {
		t.Fatalf("writeFiles: %v", err)
	}

	got, err := p.ReadFiles()

	if !errors.Is(err, clipboard.ErrPrivate) || got != nil {
		t.Errorf("ReadFiles = %q, %v, want nothing and %v", got, err, clipboard.ErrPrivate)
	}
}

func TestTenThousandFilesAndFoldersAreReadAndOneMoreIsTooMany(t *testing.T) {
	paths := make([]string, clipboard.FilesAndFoldersLimit+1)
	for i := range paths {
		paths[i] = fmt.Sprintf("/tmp/lanhop limit/file %05d.txt", i)
	}
	atTheLimit := paths[:clipboard.FilesAndFoldersLimit]

	t.Run("10 000", func(t *testing.T) {
		p := pasteboardOfThisTest(t)
		start := time.Now()
		if _, err := p.WriteFiles(atTheLimit); err != nil {
			t.Fatalf("WriteFiles: %v", err)
		}
		wrote := time.Since(start)

		got, err := p.ReadFiles()

		t.Logf("10 000 files: write %v, read %v", wrote, time.Since(start)-wrote)
		if err != nil {
			t.Fatalf("ReadFiles: %v", err)
		}
		if !slices.Equal(got, atTheLimit) {
			t.Errorf("ReadFiles gave %d paths, want the %d written, in order", len(got), len(atTheLimit))
		}
	})

	t.Run("10 001", func(t *testing.T) {
		p := pasteboardOfThisTest(t)
		start := time.Now()
		if _, err := p.WriteFiles(paths); err != nil {
			t.Fatalf("WriteFiles: %v", err)
		}
		wrote := time.Since(start)

		got, err := p.ReadFiles()

		t.Logf("10 001 files: write %v, read %v", wrote, time.Since(start)-wrote)
		if !errors.Is(err, clipboard.ErrTooManyFiles) || got != nil {
			t.Errorf("ReadFiles gave %d paths and %v, want nothing and %v", len(got), err, clipboard.ErrTooManyFiles)
		}
	})
}

func TestAFileCopyWrittenByLanhopHoldsFileURLsAndIsNotText(t *testing.T) {
	p := pasteboardOfThisTest(t)
	if _, err := p.WriteFiles([]string{"/tmp/report.pdf", "/tmp/photos"}); err != nil {
		t.Fatalf("WriteFiles: %v", err)
	}

	types := p.types()
	text, err := p.Read()

	t.Logf("types after WriteFiles: %q", types)
	if !slices.Contains(types, "public.file-url") {
		t.Errorf("types = %q, want public.file-url among them", types)
	}
	for _, plain := range []string{"public.utf8-plain-text", "public.plain-text", "NSStringPboardType"} {
		if slices.Contains(types, plain) {
			t.Errorf("types = %q, want no %s among them", types, plain)
		}
	}
	if !errors.Is(err, clipboard.ErrNotText) || text != "" {
		t.Errorf("Read = %q, %v, want nothing and %v", text, err, clipboard.ErrNotText)
	}
}

func TestWritingFilesMovesTheMark(t *testing.T) {
	p := pasteboardOfThisTest(t)
	before := p.Mark()

	mark, err := p.WriteFiles([]string{"/tmp/report.pdf"})

	if err != nil {
		t.Fatalf("WriteFiles: %v", err)
	}
	if mark == before {
		t.Errorf("the mark stayed at %d after writing files", before)
	}
	if got := p.Mark(); got != mark {
		t.Errorf("Mark = %d, want the %d that WriteFiles returned", got, mark)
	}
}

func TestNoPathsOrARelativePathIsRefusedAndLeavesThePasteboardAlone(t *testing.T) {
	cases := map[string]struct {
		paths []string
		want  error
	}{
		"no list":         {nil, clipboard.ErrEmpty},
		"an empty list":   {[]string{}, clipboard.ErrEmpty},
		"a relative path": {[]string{"/tmp/report.pdf", "photos/beach.jpg"}, ErrNotAbsolute},
		"an empty path":   {[]string{""}, ErrNotAbsolute},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			p := pasteboardOfThisTest(t)
			if _, err := p.Write("kept"); err != nil {
				t.Fatalf("Write: %v", err)
			}
			before := p.Mark()

			_, err := p.WriteFiles(c.paths)

			if !errors.Is(err, c.want) {
				t.Errorf("WriteFiles(%q): error = %v, want %v", c.paths, err, c.want)
			}
			if got := p.Mark(); got != before {
				t.Errorf("the mark moved from %d to %d", before, got)
			}
			if text, err := p.Read(); err != nil || text != "kept" {
				t.Errorf("Read = %q, %v, want the text that was there", text, err)
			}
		})
	}
}

func sameFile(a, b string) bool {
	infoA, errA := os.Stat(a)
	infoB, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(infoA, infoB)
}
