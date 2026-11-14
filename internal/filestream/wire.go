package filestream

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
)

const (
	formatVersion = 1
	headerBytes   = 13
	digestBytes   = 32
)

type kind byte

const (
	endOfCopy   kind = 0
	fileEntry   kind = 1
	folderEntry kind = 2
)

var ErrMalformed = errors.New("filestream: the file stream does not follow its format")

type header struct {
	filesAndFolders int
	bytes           int64
}

type entryStart struct {
	kind kind
	name string
	size int64
}

func writeHeader(w io.Writer, h header) error {
	var buffer [headerBytes]byte
	buffer[0] = formatVersion
	binary.BigEndian.PutUint32(buffer[1:5], uint32(h.filesAndFolders))
	binary.BigEndian.PutUint64(buffer[5:13], uint64(h.bytes))
	_, err := w.Write(buffer[:])
	return err
}

func readHeader(r io.Reader) (header, error) {
	var buffer [headerBytes]byte
	if _, err := io.ReadFull(r, buffer[:]); err != nil {
		return header{}, fmt.Errorf("filestream: reading the header: %w", err)
	}
	if buffer[0] != formatVersion {
		return header{}, fmt.Errorf("%w: format %d, want %d", ErrMalformed, buffer[0], formatVersion)
	}
	filesAndFolders := binary.BigEndian.Uint32(buffer[1:5])
	bytes := binary.BigEndian.Uint64(buffer[5:13])
	switch {
	case filesAndFolders == 0:
		return header{}, fmt.Errorf("%w: the copy holds no files", ErrMalformed)
	case filesAndFolders > clipboard.FilesAndFoldersLimit:
		return header{}, fmt.Errorf("%w: %d files and folders", clipboard.ErrTooManyFiles, filesAndFolders)
	case bytes > uint64(clipboard.FileCopyLimit):
		return header{}, fmt.Errorf("%w: %d bytes", clipboard.ErrFilesTooLarge, bytes)
	}
	return header{filesAndFolders: int(filesAndFolders), bytes: int64(bytes)}, nil
}

func writeEntryStart(w io.Writer, e entryStart) error {
	buffer := make([]byte, 0, 1+2+len(e.name)+8)
	buffer = append(buffer, byte(e.kind))
	buffer = binary.BigEndian.AppendUint16(buffer, uint16(len(e.name)))
	buffer = append(buffer, e.name...)
	buffer = binary.BigEndian.AppendUint64(buffer, uint64(e.size))
	_, err := w.Write(buffer)
	return err
}

func writeEnd(w *bufio.Writer) error {
	return w.WriteByte(byte(endOfCopy))
}

func readEntryStart(r *bufio.Reader, bytesLeft int64) (entryStart, error) {
	first, err := r.ReadByte()
	if err != nil {
		return entryStart{}, fmt.Errorf("filestream: reading an entry: %w", unexpected(err))
	}
	e := entryStart{kind: kind(first)}
	switch e.kind {
	case endOfCopy:
		return e, nil
	case fileEntry, folderEntry:
	default:
		return entryStart{}, fmt.Errorf("%w: an entry of kind %d", ErrMalformed, first)
	}
	var length [2]byte
	if _, err := io.ReadFull(r, length[:]); err != nil {
		return entryStart{}, fmt.Errorf("filestream: reading an entry: %w", unexpected(err))
	}
	nameBytes := int(binary.BigEndian.Uint16(length[:]))
	if nameBytes > pathBytesLimit {
		return entryStart{}, fmt.Errorf("%w: a path of %d bytes", ErrPathTooLong, nameBytes)
	}
	name := make([]byte, nameBytes)
	if _, err := io.ReadFull(r, name); err != nil {
		return entryStart{}, fmt.Errorf("filestream: reading an entry: %w", unexpected(err))
	}
	e.name = string(name)
	if err := wellFormedName(e.name); err != nil {
		return entryStart{}, err
	}
	var size [8]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return entryStart{}, fmt.Errorf("filestream: reading an entry: %w", unexpected(err))
	}
	declared := binary.BigEndian.Uint64(size[:])
	switch {
	case e.kind == folderEntry && declared != 0:
		return entryStart{}, fmt.Errorf("%w: the folder %q has a size of %d", ErrMalformed, e.name, declared)
	case declared > uint64(bytesLeft):
		return entryStart{}, fmt.Errorf("%w: %q holds %d bytes, more than the %d the header leaves", ErrMalformed, e.name, declared, bytesLeft)
	}
	e.size = int64(declared)
	return e, nil
}

func wellFormedName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("%w: an empty path", ErrMalformed)
	case !utf8.ValidString(name):
		return fmt.Errorf("%w: %q", ErrNameNotUTF8, name)
	case strings.HasPrefix(name, "/"):
		return fmt.Errorf("%w: the absolute path %q", ErrMalformed, name)
	case strings.ContainsRune(name, 0):
		return fmt.Errorf("%w: a path with a NUL byte", ErrMalformed)
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("%w: the path %q", ErrMalformed, name)
		}
	}
	return nil
}

func unexpected(err error) error {
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return err
}
