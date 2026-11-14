package filestream

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

var errNoDropHere = errors.New("no drop can start here")

type dropOfArrivals struct {
	*folderOfArrivals
	refusal error

	mutex       sync.Mutex
	tops        []TopEntry
	dropping    int
	beforeFirst bool
}

func newDropOfArrivals(t *testing.T) *dropOfArrivals {
	return &dropOfArrivals{folderOfArrivals: newFolderOfArrivals(t)}
}

func (d *dropOfArrivals) Dropping(tops []TopEntry) error {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	d.tops = slices.Clone(tops)
	d.dropping++
	arrived, err := os.ReadDir(d.root)
	d.beforeFirst = err == nil && len(arrived) == 0
	return d.refusal
}

func (d *dropOfArrivals) announced() (tops []TopEntry, calls int, beforeAnyEntry bool) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	return slices.Clone(d.tops), d.dropping, d.beforeFirst
}

func (b *streamBuilder) dropHeader(filesAndFolders int, total int64) *streamBuilder {
	writeHeader(b, dropFormat, header{filesAndFolders: filesAndFolders, bytes: total})
	return b
}

func (b *streamBuilder) tops(tops ...TopEntry) *streamBuilder {
	writeTops(b, tops)
	return b
}

func receiveDropFromMemory(t *testing.T, stream *streamBuilder) (*dropOfArrivals, *streamInMemory, error) {
	t.Helper()
	arrivals := newDropOfArrivals(t)
	source := &streamInMemory{Reader: bytes.NewReader(stream.Bytes())}
	err := ReceiveDrop(context.Background(), source, arrivals)
	return arrivals, source, err
}

func topFile(name string) TopEntry   { return TopEntry{Name: name} }
func topFolder(name string) TopEntry { return TopEntry{Name: name, Folder: true} }

func TestADropStreamNamesItsTopLevelFilesAndFoldersBeforeAnyOfThemArrives(t *testing.T) {
	root := realTempDir(t)
	writeFile(t, filepath.Join(root, "report", "summary.txt"), "the summary")
	writeFile(t, filepath.Join(root, "report", "charts", "march.bin"), randomContent(t, 100<<10))
	photo := writeFile(t, filepath.Join(root, "camera", "photo.jpg"), "jpeg")
	sending, receiving := dataConnections(t)
	arrivals := newDropOfArrivals(t)

	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), patience)
		defer cancel()
		done <- ReceiveDrop(ctx, receiving, arrivals)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	if _, err := SendDrop(ctx, sending, []string{filepath.Join(root, "report"), photo}); err != nil {
		t.Fatalf("SendDrop: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("ReceiveDrop: %v", err)
	}

	tops, calls, beforeAnyEntry := arrivals.announced()
	if want := []TopEntry{topFolder("report"), topFile("photo.jpg")}; !slices.Equal(tops, want) || calls != 1 {
		t.Errorf("Dropping got %+v in %d calls, want %+v once", tops, calls, want)
	}
	if !beforeAnyEntry {
		t.Error("Dropping was called after an entry had arrived")
	}
	got := treeOf(t, arrivals.root)
	if got["photo.jpg"] != "jpeg" || got["report/summary.txt"] != "the summary" || len(got) != 5 {
		t.Errorf("arrived %s", describe(got))
	}
}

func TestEachKindOfReceiveRefusesTheStreamOfTheOtherKind(t *testing.T) {
	plain := new(streamBuilder).header(1, 3).file("a.txt", "abc").end()
	if _, source, err := receiveDropFromMemory(t, plain); !errors.Is(err, ErrMalformed) || source.told {
		t.Errorf("ReceiveDrop of a stream of a file copy = %v, told %t, want ErrMalformed and nothing told", err, source.told)
	}
	drop := new(streamBuilder).dropHeader(1, 3).tops(topFile("a.txt")).file("a.txt", "abc").end()
	if _, source, err := receiveFromMemory(t, drop); !errors.Is(err, ErrMalformed) || source.told {
		t.Errorf("Receive of a drop stream = %v, told %t, want ErrMalformed and nothing told", err, source.told)
	}
}

func TestADropThatCannotStartLeavesNothingAndReadsNoEntry(t *testing.T) {
	stream := new(streamBuilder).dropHeader(1, 3).tops(topFile("a.txt")).file("a.txt", "abc").end()
	arrivals := newDropOfArrivals(t)
	arrivals.refusal = errNoDropHere
	source := &streamInMemory{Reader: bytes.NewReader(stream.Bytes())}

	err := ReceiveDrop(context.Background(), source, arrivals)

	if !errors.Is(err, errNoDropHere) || source.told {
		t.Errorf("ReceiveDrop = %v, told %t, want the refusal of the drop and nothing told", err, source.told)
	}
	if _, gaveUp := arrivals.state(); !gaveUp {
		t.Error("the arrival folder was not told to give up")
	}
	if got := treeOf(t, arrivals.root); len(got) != 0 {
		t.Errorf("the arrival folder holds %s, want nothing", describe(got))
	}
}

func TestReceiveDropRefusesTopLevelNamesThatDoNotMatchWhatArrives(t *testing.T) {
	cases := []struct {
		name   string
		stream *streamBuilder
		want   error
	}{
		{"no top-level entry", new(streamBuilder).dropHeader(1, 3).tops().file("a.txt", "abc").end(), ErrMalformed},
		{"more top-level entries than entries", new(streamBuilder).dropHeader(1, 3).tops(topFile("a.txt"), topFile("b.txt")).file("a.txt", "abc").end(), ErrMalformed},
		{"a top-level name inside a folder", new(streamBuilder).dropHeader(2, 3).tops(topFile("report/a.txt")).folder("report").file("report/a.txt", "abc").end(), ErrMalformed},
		{"a top-level name going up", new(streamBuilder).dropHeader(1, 3).tops(topFile("..")).file("a.txt", "abc").end(), ErrMalformed},
		{"a top-level name that is not UTF-8", new(streamBuilder).dropHeader(1, 3).tops(topFile("caf\xe9.txt")).file("a.txt", "abc").end(), ErrNameNotUTF8},
		{"a top-level name of 4096 bytes", new(streamBuilder).dropHeader(1, 3).tops(topFile(strings.Repeat("a", pathBytesLimit))).file("a.txt", "abc").end(), ErrPathTooLong},
		{"a top-level entry of an unknown kind", new(streamBuilder).dropHeader(1, 3).raw(0, 0, 0, 1, 7, 0, 5, 'a', '.', 't', 'x', 't').file("a.txt", "abc").end(), ErrMalformed},
		{"the same top-level name twice", new(streamBuilder).dropHeader(2, 3).tops(topFile("a.txt"), topFile("a.txt")).file("a.txt", "abc").file("b.txt", "").end(), ErrMalformed},
		{"a top-level entry that never arrives", new(streamBuilder).dropHeader(2, 3).tops(topFolder("report"), topFile("b.txt")).folder("report").file("report/a.txt", "abc").end(), ErrMalformed},
		{"another top-level entry where one was announced", new(streamBuilder).dropHeader(2, 3).tops(topFile("a.txt"), topFile("b.txt")).file("a.txt", "abc").folder("c").end(), ErrMalformed},
		{"a top-level entry that was not announced", new(streamBuilder).dropHeader(2, 3).tops(topFile("a.txt")).file("a.txt", "abc").file("b.txt", "").end(), ErrMalformed},
		{"a file announced as a folder", new(streamBuilder).dropHeader(1, 3).tops(topFolder("a.txt")).file("a.txt", "abc").end(), ErrMalformed},
		{"top-level entries in another order", new(streamBuilder).dropHeader(2, 3).tops(topFile("b.txt"), topFile("a.txt")).file("a.txt", "abc").file("b.txt", "").end(), ErrMalformed},
		{"the stream ends inside the top-level names", new(streamBuilder).dropHeader(1, 3).raw(0, 0, 0, 1, byte(fileEntry), 0, 5, 'a'), io.ErrUnexpectedEOF},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			arrivals, source, err := receiveDropFromMemory(t, c.stream)

			if !errors.Is(err, c.want) {
				t.Errorf("ReceiveDrop = %v, want %v", err, c.want)
			}
			if source.told {
				t.Error("ReceiveDrop told the other machine that every file arrived")
			}
			if _, gaveUp := arrivals.state(); !gaveUp {
				t.Error("the arrival folder was not told to give up")
			}
			if got := treeOf(t, arrivals.root); len(got) != 0 {
				t.Errorf("the arrival folder holds %s, want nothing", describe(got))
			}
		})
	}
}
