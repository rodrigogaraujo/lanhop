//go:build linux

package x11

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
)

func filesWithAwkwardNames(t *testing.T) []string {
	t.Helper()
	directory := t.TempDir()
	names := []string{
		"a file with spaces.txt",
		"relatório de ação.pdf",
		"🙂 emoji 👍🏽.png",
		"100% done.txt",
		"50%2Fhalf.txt",
		"#1 and #2.txt",
		"why? because.txt",
		"plus+comma,semicolon;equals=at@colon:.txt",
		"line one\nline two.txt",
		"carriage\r\nreturn.txt",
		"an old name \xe9t\xe9.txt",
	}
	var paths []string
	for _, name := range names {
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, []byte("content"), 0o600); err != nil {
			t.Fatalf("WriteFile %q: %v", name, err)
		}
		paths = append(paths, path)
	}
	folder := filepath.Join(directory, "a folder")
	if err := os.Mkdir(folder, 0o700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	return append(paths, folder)
}

func numberedPaths(count int, folder string) []string {
	paths := make([]string, count)
	for i := range paths {
		paths[i] = fmt.Sprintf("%s/file %05d.txt", folder, i)
	}
	return paths
}

func answerTo(t *testing.T, reader *Clipboard, target uint32) (format uint32, data string) {
	t.Helper()
	drain(reader.notifies)
	reader.c.send(newRequest(opConvertSelection, 0).u32(reader.window).u32(reader.atoms.clipboard).u32(target).u32(reader.atoms.ownProperty).u32(currentTime).done())
	deadline := time.After(time.Second)
	for {
		select {
		case got := <-reader.notifies:
			if got.target != target {
				continue
			}
			if got.property == atomNone {
				t.Fatalf("the owner refused target %d", target)
			}
			kind, value, _, err := reader.takeProperty(1024)
			if err != nil {
				t.Fatalf("takeProperty: %v", err)
			}
			return kind, string(value)
		case <-deadline:
			t.Fatalf("the owner did not answer for target %d", target)
		}
	}
}

func TestFilesWrittenByOneProgramAreReadByAnother(t *testing.T) {
	writer, reader := twoPrograms(t)
	paths := filesWithAwkwardNames(t)
	if _, err := writer.WriteFiles(paths); err != nil {
		t.Fatalf("WriteFiles: %v", err)
	}

	got, err := reader.ReadFiles()

	if err != nil {
		t.Fatalf("ReadFiles: %v", err)
	}
	if !slices.Equal(got, paths) {
		t.Errorf("ReadFiles = %q, want %q", got, paths)
	}
}

func TestAProgramReadsBackTheFilesItWroteEvenIfTheCallerChangesTheLists(t *testing.T) {
	writer, reader := twoPrograms(t)
	paths := []string{"/home/someone/report.pdf", "/home/someone/Photos"}
	written := slices.Clone(paths)
	if _, err := writer.WriteFiles(written); err != nil {
		t.Fatalf("WriteFiles: %v", err)
	}
	written[0] = "/somewhere/else"
	if got, err := writer.ReadFiles(); err == nil {
		got[1] = "/somewhere/else"
	}

	for name, board := range map[string]*Clipboard{"the writer": writer, "another program": reader} {
		if got, err := board.ReadFiles(); err != nil || !slices.Equal(got, paths) {
			t.Errorf("ReadFiles from %s = %q, %v, want %q", name, got, err, paths)
		}
	}
}

func TestAFileCopyIsOfferedOnlyAsFileListsAndNeverAsText(t *testing.T) {
	writer, reader := twoPrograms(t)
	if _, err := writer.WriteFiles([]string{"/home/someone/report.pdf"}); err != nil {
		t.Fatalf("WriteFiles: %v", err)
	}

	offered, err := reader.targetsOffered(time.Now().Add(reader.wholeRead))

	want := []uint32{reader.atoms.targets, reader.atoms.timestamp, reader.atoms.gnomeFiles, reader.atoms.uriList}
	if err != nil || !slices.Equal(offered, want) {
		t.Errorf("targets = %v, %v, want %v", offered, err, want)
	}
	for _, list := range []uint32{reader.atoms.gnomeFiles, reader.atoms.uriList} {
		if format, _ := answerTo(t, reader, list); format != list {
			t.Errorf("target %d was answered in format %d, want the same as the target", list, format)
		}
	}
}

func TestATextIsStillOfferedInTheSameFormats(t *testing.T) {
	writer, reader := twoPrograms(t)
	textFormats := []uint32{reader.atoms.targets, reader.atoms.timestamp, reader.atoms.utf8String, reader.atoms.text, reader.atoms.plainUTF8, reader.atoms.plain}
	texts := []struct {
		name    string
		text    string
		offered hint
		want    []uint32
	}{
		{"a text that fits Latin-1", "olá", noHint, append(slices.Clone(textFormats), atomString)},
		{"a text that does not fit Latin-1", "🙂", noHint, textFormats},
		{"a text marked private", "olá", secretHint, append(slices.Clone(textFormats), atomString, reader.atoms.passwordHint)},
	}
	for _, text := range texts {
		if _, err := writer.write(text.text, text.offered); err != nil {
			t.Fatalf("write: %v", err)
		}
		if offered, err := reader.targetsOffered(time.Now().Add(reader.wholeRead)); err != nil || !slices.Equal(offered, text.want) {
			t.Errorf("%s: targets = %v, %v, want %v", text.name, offered, err, text.want)
		}
	}

	writer.Write("olá")
	answers := []struct {
		target, format uint32
		data           string
	}{
		{reader.atoms.utf8String, reader.atoms.utf8String, "olá"},
		{reader.atoms.text, reader.atoms.utf8String, "olá"},
		{reader.atoms.plainUTF8, reader.atoms.plainUTF8, "olá"},
		{reader.atoms.plain, reader.atoms.plain, "olá"},
		{atomString, atomString, "ol\xe1"},
	}
	for _, want := range answers {
		if format, data := answerTo(t, reader, want.target); format != want.format || data != want.data {
			t.Errorf("target %d gave %q in format %d, want %q in format %d", want.target, data, format, want.data, want.format)
		}
	}
}

func TestTheGNOMEListIsPreferredEvenWhenTheAddressListIsOfferedFirst(t *testing.T) {
	writer, reader := twoPrograms(t)
	addressListFirst := heldCopy{offers: []offer{
		{target: writer.atoms.uriList, kind: writer.atoms.uriList, data: []byte("file:///from/the/address/list\r\n")},
		{target: writer.atoms.gnomeFiles, kind: writer.atoms.gnomeFiles, data: []byte("copy\nfile:///from/the/gnome/list")},
	}}
	if _, err := writer.own(addressListFirst); err != nil {
		t.Fatalf("own: %v", err)
	}
	offered, err := reader.targetsOffered(time.Now().Add(reader.wholeRead))
	if err != nil || slices.Index(offered, reader.atoms.uriList) > slices.Index(offered, reader.atoms.gnomeFiles) {
		t.Fatalf("targets = %v, %v, want the address list before the GNOME list", offered, err)
	}

	if got, err := reader.ReadFiles(); err != nil || !slices.Equal(got, []string{"/from/the/gnome/list"}) {
		t.Errorf("ReadFiles = %q, %v, want the path of the GNOME list", got, err)
	}
}

func TestReadingTextFromAFileCopyGivesNotText(t *testing.T) {
	writer, reader := twoPrograms(t)
	if _, err := writer.WriteFiles([]string{"/home/someone/report.pdf"}); err != nil {
		t.Fatalf("WriteFiles: %v", err)
	}

	for name, board := range map[string]*Clipboard{"the writer": writer, "another program": reader} {
		if got, err := board.Read(); !errors.Is(err, clipboard.ErrNotText) || got != "" {
			t.Errorf("Read from %s = %q, %v, want nothing and %v", name, got, err, clipboard.ErrNotText)
		}
	}
}

func TestATextWrittenAfterFilesReplacesThemAndTheReverse(t *testing.T) {
	writer, reader := twoPrograms(t)
	paths := []string{"/home/someone/report.pdf"}

	writer.WriteFiles(paths)
	writer.Write("hello")
	if got, err := reader.Read(); err != nil || got != "hello" {
		t.Errorf("Read after files then text = %q, %v, want %q", got, err, "hello")
	}
	if _, err := reader.ReadFiles(); !errors.Is(err, clipboard.ErrNotFiles) {
		t.Errorf("ReadFiles after files then text: error = %v, want %v", err, clipboard.ErrNotFiles)
	}

	writer.WriteFiles(paths)
	if got, err := reader.ReadFiles(); err != nil || !slices.Equal(got, paths) {
		t.Errorf("ReadFiles after text then files = %q, %v, want %q", got, err, paths)
	}
}

func TestWritingFilesMovesTheMark(t *testing.T) {
	writer, watcher := twoPrograms(t)
	before := watcher.Mark()

	mark, err := writer.WriteFiles([]string{"/home/someone/report.pdf"})

	if err != nil {
		t.Fatalf("WriteFiles: %v", err)
	}
	if watcher.Mark() == before {
		t.Error("the mark of another program did not move when files were written")
	}
	if got := writer.Mark(); got != mark {
		t.Errorf("Mark after the write = %d, want the %d that WriteFiles returned", got, mark)
	}
}

func TestWritingNoFilesARelativePathOrANULByteIsRefusedAndLeavesTheClipboardAsItWas(t *testing.T) {
	writer, reader := twoPrograms(t)
	writer.Write("kept")
	before := reader.Mark()

	if _, err := writer.WriteFiles(nil); !errors.Is(err, clipboard.ErrEmpty) {
		t.Errorf("WriteFiles with no paths: error = %v, want %v", err, clipboard.ErrEmpty)
	}
	if _, err := writer.WriteFiles([]string{"/home/someone/a.txt", "relative/b.txt"}); !errors.Is(err, ErrNotAbsolute) {
		t.Errorf("WriteFiles with a relative path: error = %v, want %v", err, ErrNotAbsolute)
	}
	if _, err := writer.WriteFiles([]string{"/home/someone/a.txt", "/home/someone/nul\x00byte.txt"}); !errors.Is(err, ErrNULInPath) {
		t.Errorf("WriteFiles with a NUL byte in a path: error = %v, want %v", err, ErrNULInPath)
	}

	if reader.Mark() != before {
		t.Error("a refused write of files moved the mark")
	}
	if got, err := reader.Read(); err != nil || got != "kept" {
		t.Errorf("Read = %q, %v, want the text written before", got, err)
	}
}

func TestACopyThatIsNotAListOfLocalFilesHoldsNoFiles(t *testing.T) {
	uriList := func(b *Clipboard) uint32 { return b.atoms.uriList }
	gnomeFiles := func(b *Clipboard) uint32 { return b.atoms.gnomeFiles }
	lists := map[string]struct {
		format func(b *Clipboard) uint32
		data   string
	}{
		"a list with a web address":           {uriList, "file:///home/someone/a.txt\r\nhttp://example.org/b\r\n"},
		"a file on another host":              {uriList, "file://otherhost/home/someone/a.txt\r\n"},
		"a file address with a relative path": {uriList, "file:relative/a.txt\r\n"},
		"a relative path with no scheme":      {uriList, "relative/a.txt\r\n"},
		"an escape that cannot be decoded":    {uriList, "file:///home/someone/bad%zzname\r\n"},
		"a NUL byte in the path":              {uriList, "file:///home/someone/nul%00byte\r\n"},
		"a fragment after the path":           {uriList, "file:///home/someone/a.txt#part\r\n"},
		"a query after the path":              {uriList, "file:///home/someone/a.txt?x=1\r\n"},
		"a user name before the host":         {uriList, "file://someone@localhost/home/someone/a.txt\r\n"},
		"a slash escaped as %2F":              {uriList, "file:///home/someone/a%2Fb.txt\r\n"},
		"a slash escaped as %2f":              {uriList, "file:///home/someone/a%2fb.txt\r\n"},
		"only a comment":                      {uriList, "# nothing here\r\n"},
		"a GNOME list with no file":           {gnomeFiles, "copy\n"},
		"a GNOME list with an unknown action": {gnomeFiles, "move\nfile:///home/someone/a.txt"},
		"a GNOME list with no action":         {gnomeFiles, "file:///home/someone/a.txt"},
	}
	for name, list := range lists {
		t.Run(name, func(t *testing.T) {
			writer, reader := twoPrograms(t)
			offering(writer, list.format, list.data)
			writer.Write("/home/someone/a.txt")

			if got, err := reader.ReadFiles(); !errors.Is(err, clipboard.ErrNotFiles) || got != nil {
				t.Errorf("ReadFiles = %q, %v, want nothing and %v", got, err, clipboard.ErrNotFiles)
			}
		})
	}
}

func TestATextCopyHoldsNoFiles(t *testing.T) {
	writer, reader := twoPrograms(t)
	writer.Write("/home/someone/report.pdf")

	for name, board := range map[string]*Clipboard{"the writer": writer, "another program": reader} {
		if got, err := board.ReadFiles(); !errors.Is(err, clipboard.ErrNotFiles) || got != nil {
			t.Errorf("ReadFiles from %s = %q, %v, want nothing and %v", name, got, err, clipboard.ErrNotFiles)
		}
	}
}

func TestAClipboardThatNobodyOwnsHoldsNoFiles(t *testing.T) {
	_, reader := twoPrograms(t)

	if got, err := reader.ReadFiles(); !errors.Is(err, clipboard.ErrNotFiles) || got != nil {
		t.Errorf("ReadFiles = %q, %v, want nothing and %v", got, err, clipboard.ErrNotFiles)
	}
}

func TestAFileCopyMarkedPrivateIsNotRead(t *testing.T) {
	for name, offered := range map[string]hint{"marked secret": secretHint, "with a hint that cannot be read": refusedHint} {
		t.Run(name, func(t *testing.T) {
			writer, reader := twoPrograms(t)
			if _, err := writer.writeFiles([]string{"/home/someone/passwords.kdbx"}, offered); err != nil {
				t.Fatalf("writeFiles: %v", err)
			}

			for who, board := range map[string]*Clipboard{"the writer": writer, "another program": reader} {
				if got, err := board.ReadFiles(); !errors.Is(err, clipboard.ErrPrivate) || got != nil {
					t.Errorf("ReadFiles from %s = %q, %v, want nothing and %v", who, got, err, clipboard.ErrPrivate)
				}
			}
		})
	}
}

func TestTenThousandFilesAreReadAndOneMoreIsTooMany(t *testing.T) {
	counts := map[int]error{clipboard.FilesAndFoldersLimit: nil, clipboard.FilesAndFoldersLimit + 1: clipboard.ErrTooManyFiles}
	for count, want := range counts {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			writer, reader := twoPrograms(t)
			paths := numberedPaths(count, "/home/someone/arrived")
			if _, err := writer.WriteFiles(paths); err != nil {
				t.Fatalf("WriteFiles: %v", err)
			}

			got, err := reader.ReadFiles()

			switch {
			case want == nil && (err != nil || !slices.Equal(got, paths)):
				t.Errorf("ReadFiles gave %d paths, %v, want the same %d paths", len(got), err, len(paths))
			case want != nil && (!errors.Is(err, want) || got != nil):
				t.Errorf("ReadFiles gave %d paths, %v, want nothing and %v", len(got), err, want)
			}
		})
	}
}

func TestTenThousandLongPathsFitInTheLimitOfAFileList(t *testing.T) {
	writer, reader := twoPrograms(t)
	paths := numberedPaths(clipboard.FilesAndFoldersLimit, "/home/someone"+strings.Repeat("/a long folder name", 50))
	if _, err := writer.WriteFiles(paths); err != nil {
		t.Fatalf("WriteFiles: %v", err)
	}

	got, err := reader.ReadFiles()

	if err != nil || !slices.Equal(got, paths) {
		t.Errorf("ReadFiles gave %d paths, %v, want the same %d paths of %d bytes", len(got), err, len(paths), len(paths[0]))
	}
}

func TestAFileListLongerThanItsLimitCountsAsTooManyFiles(t *testing.T) {
	writer, reader := twoPrograms(t)
	paths := numberedPaths(clipboard.FilesAndFoldersLimit/2, "/home/someone"+strings.Repeat("/deep", 800))
	if _, err := writer.WriteFiles(paths); err != nil {
		t.Fatalf("WriteFiles: %v", err)
	}

	if got, err := reader.ReadFiles(); !errors.Is(err, clipboard.ErrTooManyFiles) || got != nil {
		t.Errorf("ReadFiles gave %d paths, %v, want nothing and %v", len(got), err, clipboard.ErrTooManyFiles)
	}
}

func TestAListOfMillionsOfLinesIsReadWithoutKeepingEachLine(t *testing.T) {
	lists := []struct {
		name string
		line string
		want error
	}{
		{"a word on each line", "a\n", clipboard.ErrNotFiles},
		{"a comment on each line", "#\n", clipboard.ErrNotFiles},
		{"a file address on each line", "file:///a\n", clipboard.ErrTooManyFiles},
	}
	for _, list := range lists {
		t.Run(list.name, func(t *testing.T) {
			whole := strings.Repeat(list.line, fileListLimit/len(list.line))
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)

			_, err := pathsInList(whole, false)

			runtime.ReadMemStats(&after)
			allocated := after.TotalAlloc - before.TotalAlloc
			t.Logf("a list of %d bytes allocated %d bytes", len(whole), allocated)
			if !errors.Is(err, list.want) {
				t.Errorf("pathsInList: error = %v, want %v", err, list.want)
			}
			if allocated > 8<<20 {
				t.Errorf("a list of %d bytes allocated %d bytes, want at most 8 MiB", len(whole), allocated)
			}
		})
	}
}

func TestAnOwnerThatNeverAnswersAReadOfFilesIsReportedInsteadOfAwaited(t *testing.T) {
	display := startXvfb(t)
	reader := openOn(t, display)
	ownedByAnotherProgram(t, display, reader, neverAnswering)
	reader.readTimeout = 200 * time.Millisecond

	start := time.Now()
	_, err := reader.ReadFiles()

	if !errors.Is(err, ErrNoAnswer) {
		t.Errorf("ReadFiles: error = %v, want %v", err, ErrNoAnswer)
	}
	if waited := time.Since(start); waited > time.Second {
		t.Errorf("ReadFiles waited %v", waited)
	}
}

type xclip struct {
	tool    string
	display string
}

func xclipOn(t *testing.T, display string) xclip {
	t.Helper()
	tool, err := exec.LookPath("xclip")
	if err != nil {
		t.Skip("xclip is not installed")
	}
	return xclip{tool: tool, display: display}
}

func (x xclip) command(t *testing.T, arguments ...string) *exec.Cmd {
	t.Helper()
	running, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	command := exec.CommandContext(running, x.tool, append([]string{"-selection", "clipboard"}, arguments...)...)
	command.Env = append(os.Environ(), "DISPLAY="+x.display)
	return command
}

func (x xclip) read(t *testing.T, target string) (string, error) {
	t.Helper()
	got, err := x.command(t, "-t", target, "-o").Output()
	return string(got), err
}

func (x xclip) copy(t *testing.T, watcher *Clipboard, target, data string) {
	t.Helper()
	before := watcher.Mark()
	write := x.command(t, "-t", target, "-i")
	write.Stdin = strings.NewReader(data)
	if err := write.Run(); err != nil {
		t.Fatalf("xclip -i -t %s: %v", target, err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for watcher.Mark() == before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if watcher.Mark() == before {
		t.Fatalf("xclip -i -t %s did not take the clipboard", target)
	}
}

func TestAnotherX11ProgramReadsTheFileListsLanhopWroteAndNoText(t *testing.T) {
	display := startXvfb(t)
	x := xclipOn(t, display)
	board := openOn(t, display)
	if _, err := board.WriteFiles([]string{"/home/someone/a b/ação #1?.txt", "/home/someone/100% sure\n🙂", "/home/someone/folder"}); err != nil {
		t.Fatalf("WriteFiles: %v", err)
	}
	addresses := []string{
		"file:///home/someone/a%20b/a%C3%A7%C3%A3o%20%231%3F.txt",
		"file:///home/someone/100%25%20sure%0A%F0%9F%99%82",
		"file:///home/someone/folder",
	}
	lists := map[string]string{
		"text/uri-list":                strings.Join(addresses, "\r\n") + "\r\n",
		"x-special/gnome-copied-files": "copy\n" + strings.Join(addresses, "\n"),
	}

	for target, want := range lists {
		if got, err := x.read(t, target); err != nil || got != want {
			t.Errorf("xclip -t %s -o = %q, %v, want %q", target, got, err, want)
		}
	}
	for _, text := range []string{"UTF8_STRING", "STRING", "TEXT", "text/plain"} {
		if got, err := x.read(t, text); err == nil && got != "" {
			t.Errorf("xclip -t %s -o = %q, want nothing", text, got)
		}
	}
}

func TestFileListsWrittenByAnotherX11ProgramAreRead(t *testing.T) {
	display := startXvfb(t)
	x := xclipOn(t, display)
	board := openOn(t, display)
	lists := []struct {
		name   string
		target string
		data   string
		paths  []string
	}{
		{
			"a GNOME copy", "x-special/gnome-copied-files",
			"copy\nfile:///home/someone/report.pdf\nfile:///home/someone/Fotos%20de%20f%C3%A9rias",
			[]string{"/home/someone/report.pdf", "/home/someone/Fotos de férias"},
		},
		{
			"a GNOME cut, read as a copy", "x-special/gnome-copied-files",
			"cut\nfile:///home/someone/moved.txt\n",
			[]string{"/home/someone/moved.txt"},
		},
		{
			"a GNOME copy with CRLF", "x-special/gnome-copied-files",
			"copy\r\nfile:///home/someone/one.txt\r\nfile:///home/someone/two.txt\r\n",
			[]string{"/home/someone/one.txt", "/home/someone/two.txt"},
		},
		{
			"an address list with a comment and CRLF", "text/uri-list",
			"# copied by a test\r\nfile:///home/someone/a%23b.txt\r\nfile://localhost/home/someone/notes.txt\r\nFILE:///home/someone/upper.txt\r\nfile://LOCALHOST/home/someone/loud.txt\r\n",
			[]string{"/home/someone/a#b.txt", "/home/someone/notes.txt", "/home/someone/upper.txt", "/home/someone/loud.txt"},
		},
		{
			"an address list with LF only and a blank line", "text/uri-list",
			"file:///tmp/one\n\nfile:/tmp/two\n",
			[]string{"/tmp/one", "/tmp/two"},
		},
	}
	for _, list := range lists {
		t.Run(list.name, func(t *testing.T) {
			x.copy(t, board, list.target, list.data)

			if got, err := board.ReadFiles(); err != nil || !slices.Equal(got, list.paths) {
				t.Errorf("ReadFiles = %q, %v, want %q", got, err, list.paths)
			}
		})
	}
}

func TestALargeFileListTravelsInPartsBothWaysWithAnotherX11Program(t *testing.T) {
	display := startXvfb(t)
	x := xclipOn(t, display)
	board := openOn(t, display)
	paths := numberedPaths(clipboard.FilesAndFoldersLimit, "/home/someone/"+strings.Repeat("folder-", 70))
	for i := range paths {
		paths[i] = strings.ReplaceAll(paths[i], " ", "-")
	}
	var list strings.Builder
	for _, path := range paths {
		list.WriteString("file://" + path + "\r\n")
	}
	if list.Len() <= 4<<20 {
		t.Fatalf("the list has %d bytes, too few to need several parts", list.Len())
	}

	if _, err := board.WriteFiles(paths); err != nil {
		t.Fatalf("WriteFiles: %v", err)
	}
	if got, err := x.read(t, "text/uri-list"); err != nil || got != list.String() {
		t.Errorf("xclip read %d bytes, %v, want the %d bytes of the list lanhop wrote", len(got), err, list.Len())
	}

	x.copy(t, board, "text/uri-list", list.String())
	if got, err := board.ReadFiles(); err != nil || !slices.Equal(got, paths) {
		t.Errorf("ReadFiles gave %d paths, %v, want the %d paths xclip wrote", len(got), err, len(paths))
	}
}
