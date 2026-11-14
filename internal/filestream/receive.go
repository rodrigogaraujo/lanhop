package filestream

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"path"
	"time"
)

var ErrDigest = errors.New("filestream: a file arrived with another SHA-256 than the one sent")

type Source interface {
	io.Reader
	SetDeadline(deadline time.Time) error
	TellAllArrived() error
}

type Arrivals interface {
	Expect(bytes int64, filesAndFolders int) error
	Folder(name string) error
	File(name string, size int64) (IncomingFile, error)
	Finish() error
	GiveUp()
}

type IncomingFile interface {
	io.Writer
	Whole() error
}

type DropArrivals interface {
	Arrivals
	Dropping(tops []TopEntry) error
}

func Receive(ctx context.Context, conn Source, into Arrivals) error {
	return receive(ctx, conn, into, formatVersion, nil)
}

func ReceiveDrop(ctx context.Context, conn Source, into DropArrivals) error {
	return receive(ctx, conn, into, dropFormat, into.Dropping)
}

func receive(ctx context.Context, conn Source, into Arrivals, format byte, dropping func(tops []TopEntry) error) (err error) {
	stopReading := context.AfterFunc(ctx, func() { conn.SetDeadline(time.Now()) })
	defer stopReading()
	defer func() {
		if err != nil {
			into.GiveUp()
			err = because(ctx, err)
		}
	}()
	r := bufio.NewReaderSize(conn, streamBuffer)
	h, err := readHeader(r, format)
	if err != nil {
		return err
	}
	if err := into.Expect(h.bytes, h.filesAndFolders); err != nil {
		return err
	}
	var expected *topsExpected
	if dropping != nil {
		tops, err := readTops(r, h)
		if err != nil {
			return err
		}
		if err := dropping(tops); err != nil {
			return err
		}
		expected = &topsExpected{tops: tops}
	}
	if err := receiveEntries(r, h, expected, into); err != nil {
		return err
	}
	if err := into.Finish(); err != nil {
		return err
	}
	if err := conn.TellAllArrived(); err != nil {
		return fmt.Errorf("filestream: telling the other machine that every file arrived: %w", err)
	}
	return nil
}

func receiveEntries(r *bufio.Reader, h header, expected *topsExpected, into Arrivals) error {
	var openFolders []string
	bytesLeft := h.bytes
	digest := sha256.New()
	var sent [digestBytes]byte
	for received := range h.filesAndFolders {
		e, err := readEntryStart(r, bytesLeft)
		if err != nil {
			return err
		}
		if e.kind == endOfCopy {
			return fmt.Errorf("%w: the copy ended after %d of the %d files and folders", ErrMalformed, received, h.filesAndFolders)
		}
		if openFolders, err = insideAnOpenFolder(openFolders, e.name); err != nil {
			return err
		}
		if err := expected.arrived(e.name, e.kind == folderEntry); err != nil {
			return err
		}
		if e.kind == folderEntry {
			if err := into.Folder(e.name); err != nil {
				return err
			}
			openFolders = append(openFolders, e.name)
			continue
		}
		file, err := into.File(e.name, e.size)
		if err != nil {
			return err
		}
		digest.Reset()
		if _, err := io.CopyN(io.MultiWriter(file, digest), r, e.size); err != nil {
			return fmt.Errorf("filestream: receiving %q: %w", e.name, unexpected(err))
		}
		if _, err := io.ReadFull(r, sent[:]); err != nil {
			return fmt.Errorf("filestream: receiving %q: %w", e.name, unexpected(err))
		}
		if !bytes.Equal(digest.Sum(nil), sent[:]) {
			return fmt.Errorf("%w: %q", ErrDigest, e.name)
		}
		if err := file.Whole(); err != nil {
			return err
		}
		bytesLeft -= e.size
	}
	if bytesLeft != 0 {
		return fmt.Errorf("%w: the files held %d bytes fewer than the header said", ErrMalformed, bytesLeft)
	}
	if err := expected.allArrived(); err != nil {
		return err
	}
	last, err := r.ReadByte()
	if err != nil {
		return fmt.Errorf("filestream: reading the end of the copy: %w", unexpected(err))
	}
	if kind(last) != endOfCopy {
		return fmt.Errorf("%w: more than the %d files and folders the header said", ErrMalformed, h.filesAndFolders)
	}
	return nil
}

func insideAnOpenFolder(openFolders []string, name string) ([]string, error) {
	parent := path.Dir(name)
	for len(openFolders) > 0 && openFolders[len(openFolders)-1] != parent {
		openFolders = openFolders[:len(openFolders)-1]
	}
	if parent != "." && len(openFolders) == 0 {
		return nil, fmt.Errorf("%w: %q arrived outside the folder before it", ErrMalformed, name)
	}
	return openFolders, nil
}
