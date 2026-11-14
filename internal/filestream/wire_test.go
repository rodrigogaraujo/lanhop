package filestream

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
)

type streamInMemory struct {
	*bytes.Reader
	told      bool
	tellFails error
}

func (s *streamInMemory) SetDeadline(time.Time) error { return nil }

func (s *streamInMemory) TellAllArrived() error {
	if s.tellFails != nil {
		return s.tellFails
	}
	s.told = true
	return nil
}

type streamBuilder struct {
	bytes.Buffer
}

func (b *streamBuilder) header(filesAndFolders int, total int64) *streamBuilder {
	writeHeader(b, header{filesAndFolders: filesAndFolders, bytes: total})
	return b
}

func (b *streamBuilder) folder(name string) *streamBuilder {
	writeEntryStart(b, entryStart{kind: folderEntry, name: name})
	return b
}

func (b *streamBuilder) file(name, content string) *streamBuilder {
	writeEntryStart(b, entryStart{kind: fileEntry, name: name, size: int64(len(content))})
	b.WriteString(content)
	digest := sha256.Sum256([]byte(content))
	b.Write(digest[:])
	return b
}

func (b *streamBuilder) raw(data ...byte) *streamBuilder {
	b.Write(data)
	return b
}

func (b *streamBuilder) end() *streamBuilder {
	return b.raw(byte(endOfCopy))
}

func receiveFromMemory(t *testing.T, stream *streamBuilder) (*folderOfArrivals, *streamInMemory, error) {
	t.Helper()
	arrivals := newFolderOfArrivals(t)
	source := &streamInMemory{Reader: bytes.NewReader(stream.Bytes())}
	err := Receive(context.Background(), source, arrivals)
	return arrivals, source, err
}

func TestAWellFormedStreamInMemoryArrivesAndIsAcknowledged(t *testing.T) {
	stream := new(streamBuilder).header(3, 7).folder("report").file("report/a.txt", "abc").file("b.txt", "defg").end()

	arrivals, source, err := receiveFromMemory(t, stream)

	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if !source.told {
		t.Error("Receive did not tell the other machine that every file arrived")
	}
	want := map[string]string{"report/": "", "report/a.txt": "abc", "b.txt": "defg"}
	if got := treeOf(t, arrivals.root); describe(got) != describe(want) {
		t.Errorf("arrived %s, want %s", describe(got), describe(want))
	}
}

func TestReceiveRefusesAStreamThatBreaksItsFormat(t *testing.T) {
	cases := []struct {
		name   string
		stream *streamBuilder
		want   error
	}{
		{"another format", new(streamBuilder).raw(2, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0).file("a", "").end(), ErrMalformed},
		{"no files", new(streamBuilder).header(0, 0).end(), ErrMalformed},
		{"more than 10000 entries in the header", new(streamBuilder).header(clipboard.FilesAndFoldersLimit+1, 0), clipboard.ErrTooManyFiles},
		{"more than 4 GiB in the header", new(streamBuilder).header(1, clipboard.FileCopyLimit+1), clipboard.ErrFilesTooLarge},
		{"one entry more than the header says", new(streamBuilder).header(1, 3).file("a.txt", "abc").file("b.txt", "").end(), ErrMalformed},
		{"one entry fewer than the header says", new(streamBuilder).header(2, 3).file("a.txt", "abc").end(), ErrMalformed},
		{"a byte more in a file than its size", new(streamBuilder).header(1, 3).raw(byte(fileEntry), 0, 5, 'a', '.', 't', 'x', 't', 0, 0, 0, 0, 0, 0, 0, 3, 'a', 'b', 'c', 'd').raw(make([]byte, digestBytes)...).end(), ErrDigest},
		{"a file larger than the header leaves", new(streamBuilder).header(1, 2).file("a.txt", "abc").end(), ErrMalformed},
		{"files smaller than the header says", new(streamBuilder).header(1, 4).file("a.txt", "abc").end(), ErrMalformed},
		{"another digest", new(streamBuilder).header(1, 3).raw(byte(fileEntry), 0, 5, 'a', '.', 't', 'x', 't', 0, 0, 0, 0, 0, 0, 0, 3, 'a', 'b', 'c').raw(make([]byte, digestBytes)...).end(), ErrDigest},
		{"a folder with a size", new(streamBuilder).header(1, 3).raw(byte(folderEntry), 0, 1, 'f', 0, 0, 0, 0, 0, 0, 0, 3).end(), ErrMalformed},
		{"an unknown kind of entry", new(streamBuilder).header(1, 0).raw(7, 0, 1, 'f', 0, 0, 0, 0, 0, 0, 0, 0).end(), ErrMalformed},
		{"a path longer than 4096 bytes", new(streamBuilder).header(1, 0).raw(byte(folderEntry), 0x10, 0x01), ErrPathTooLong},
		{"an absolute path", new(streamBuilder).header(1, 3).file("/etc/passwd", "abc").end(), ErrMalformed},
		{"a path going up", new(streamBuilder).header(1, 3).file("../outside.txt", "abc").end(), ErrMalformed},
		{"a path going up inside", new(streamBuilder).header(2, 3).folder("report").file("report/../../outside.txt", "abc").end(), ErrMalformed},
		{"an empty path", new(streamBuilder).header(1, 3).file("", "abc").end(), ErrMalformed},
		{"a path with a dot", new(streamBuilder).header(2, 3).folder("report").file("report/./a.txt", "abc").end(), ErrMalformed},
		{"a path with a NUL byte", new(streamBuilder).header(1, 3).file("a\x00.txt", "abc").end(), ErrMalformed},
		{"a name that is not UTF-8", new(streamBuilder).header(1, 3).file("caf\xe9.txt", "abc").end(), ErrNameNotUTF8},
		{"a file before its folder", new(streamBuilder).header(1, 3).file("report/a.txt", "abc").end(), ErrMalformed},
		{"a file under a file", new(streamBuilder).header(2, 6).file("report", "abc").file("report/a.txt", "abc").end(), ErrMalformed},
		{"a file under a folder already left", new(streamBuilder).header(4, 3).folder("report").folder("report/a").folder("report/b").file("report/a/late.txt", "abc").end(), ErrMalformed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			arrivals, source, err := receiveFromMemory(t, c.stream)

			if !errors.Is(err, c.want) {
				t.Errorf("Receive = %v, want %v", err, c.want)
			}
			if source.told {
				t.Error("Receive told the other machine that every file arrived")
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

func TestReceiveTakesADeepTreeBackAndForth(t *testing.T) {
	stream := new(streamBuilder).header(6, 2).
		folder("a").folder("a/b").folder("a/b/c").file("a/b/c/x", "x").
		folder("a/d").file("a/y", "y").end()

	arrivals, _, err := receiveFromMemory(t, stream)

	if err != nil {
		t.Fatalf("Receive of a tree that goes down, up and down again: %v", err)
	}
	want := map[string]string{"a/": "", "a/b/": "", "a/b/c/": "", "a/b/c/x": "x", "a/d/": "", "a/y": "y"}
	if got := treeOf(t, arrivals.root); describe(got) != describe(want) {
		t.Errorf("arrived %s, want %s", describe(got), describe(want))
	}
}
