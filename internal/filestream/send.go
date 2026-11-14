package filestream

import (
	"bufio"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"time"
)

const streamBuffer = 64 << 10

type Destination interface {
	io.Writer
	SetWriteDeadline(deadline time.Time) error
	AwaitAllArrived(ctx context.Context) error
}

func Send(ctx context.Context, conn Destination, paths []string) (Listing, error) {
	stopWriting := context.AfterFunc(ctx, func() { conn.SetWriteDeadline(time.Now()) })
	defer stopWriting()
	l := newLister(ctx.Err)
	if err := l.list(paths); err != nil {
		return l.listing, err
	}
	err := send(ctx, conn, l.entries, l.listing)
	return l.listing, because(ctx, err)
}

func send(ctx context.Context, conn Destination, entries []entry, listing Listing) error {
	w := bufio.NewWriterSize(conn, streamBuffer)
	if err := writeHeader(w, header{filesAndFolders: len(entries), bytes: listing.Copy.Bytes}); err != nil {
		return fmt.Errorf("filestream: writing the header: %w", err)
	}
	buffer := make([]byte, streamBuffer)
	for _, e := range entries {
		if e.folder {
			if err := writeEntryStart(w, entryStart{kind: folderEntry, name: e.name}); err != nil {
				return fmt.Errorf("filestream: sending %s: %w", e.path, err)
			}
			continue
		}
		if err := writeEntryStart(w, entryStart{kind: fileEntry, name: e.name, size: e.info.Size()}); err != nil {
			return fmt.Errorf("filestream: sending %s: %w", e.path, err)
		}
		digest, err := sendFile(w, e, buffer)
		if err != nil {
			return err
		}
		if _, err := w.Write(digest); err != nil {
			return fmt.Errorf("filestream: sending %s: %w", e.path, err)
		}
	}
	if err := writeEnd(w); err != nil {
		return fmt.Errorf("filestream: ending the copy: %w", err)
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("filestream: ending the copy: %w", err)
	}
	if err := conn.AwaitAllArrived(ctx); err != nil {
		return fmt.Errorf("filestream: waiting for the other machine to hold every file: %w", err)
	}
	return nil
}

func sendFile(w io.Writer, e entry, buffer []byte) ([]byte, error) {
	file, err := openWithoutFollowing(e.path)
	if err != nil {
		return nil, openingFailed(e.path, err)
	}
	defer file.Close()
	if err := stillAsListed(file, e); err != nil {
		return nil, err
	}
	digest := sha256.New()
	copied, err := io.CopyBuffer(io.MultiWriter(w, digest), io.LimitReader(file, e.info.Size()), buffer)
	if err != nil {
		return nil, fmt.Errorf("filestream: sending %s: %w", e.path, err)
	}
	if copied < e.info.Size() {
		return nil, fmt.Errorf("%w: %s", ErrChanged, e.path)
	}
	if grew, _ := file.Read(buffer[:1]); grew > 0 {
		return nil, fmt.Errorf("%w: %s", ErrChanged, e.path)
	}
	if err := stillAsListed(file, e); err != nil {
		return nil, err
	}
	return digest.Sum(nil), nil
}

func stillAsListed(file *os.File, e entry) error {
	now, err := file.Stat()
	if err != nil {
		return fmt.Errorf("filestream: %w", err)
	}
	if !sameAsListed(e.info, now) {
		return fmt.Errorf("%w: %s", ErrChanged, e.path)
	}
	return nil
}

func because(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if stopped := ctx.Err(); stopped != nil {
		return fmt.Errorf("%w: %w", stopped, err)
	}
	return err
}
