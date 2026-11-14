package filestream

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
)

const (
	formatVersion byte = 1
	dropFormat    byte = 2
	headerBytes        = 13
	digestBytes        = 32
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

type TopEntry struct {
	Name   string
	Folder bool
}

type entryStart struct {
	kind kind
	name string
	size int64
}

func writeHeader(w io.Writer, format byte, h header) error {
	var buffer [headerBytes]byte
	buffer[0] = format
	binary.BigEndian.PutUint32(buffer[1:5], uint32(h.filesAndFolders))
	binary.BigEndian.PutUint64(buffer[5:13], uint64(h.bytes))
	_, err := w.Write(buffer[:])
	return err
}

func readHeader(r io.Reader, format byte) (header, error) {
	var buffer [headerBytes]byte
	if _, err := io.ReadFull(r, buffer[:]); err != nil {
		return header{}, fmt.Errorf("filestream: reading the header: %w", err)
	}
	if buffer[0] != format {
		return header{}, fmt.Errorf("%w: format %d, want %d", ErrMalformed, buffer[0], format)
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

func topsOf(entries []entry) []TopEntry {
	var tops []TopEntry
	for _, e := range entries {
		if !strings.Contains(e.name, "/") {
			tops = append(tops, TopEntry{Name: e.name, Folder: e.folder})
		}
	}
	return tops
}

func writeTops(w io.Writer, tops []TopEntry) error {
	buffer := binary.BigEndian.AppendUint32(nil, uint32(len(tops)))
	for _, top := range tops {
		entryKind := fileEntry
		if top.Folder {
			entryKind = folderEntry
		}
		buffer = append(buffer, byte(entryKind))
		buffer = binary.BigEndian.AppendUint16(buffer, uint16(len(top.Name)))
		buffer = append(buffer, top.Name...)
	}
	_, err := w.Write(buffer)
	return err
}

func readTops(r io.Reader, h header) ([]TopEntry, error) {
	var count [4]byte
	if _, err := io.ReadFull(r, count[:]); err != nil {
		return nil, fmt.Errorf("filestream: reading the top-level files and folders: %w", unexpected(err))
	}
	announced := binary.BigEndian.Uint32(count[:])
	if announced == 0 || announced > uint32(h.filesAndFolders) {
		return nil, fmt.Errorf("%w: %d top-level files and folders in a copy of %d", ErrMalformed, announced, h.filesAndFolders)
	}
	tops := make([]TopEntry, 0, announced)
	seen := make(map[string]bool, announced)
	for range announced {
		top, err := readTop(r)
		if err != nil {
			return nil, err
		}
		if seen[top.Name] {
			return nil, fmt.Errorf("%w: %q is announced twice", ErrMalformed, top.Name)
		}
		seen[top.Name] = true
		tops = append(tops, top)
	}
	return tops, nil
}

func readTop(r io.Reader) (TopEntry, error) {
	var start [3]byte
	if _, err := io.ReadFull(r, start[:]); err != nil {
		return TopEntry{}, fmt.Errorf("filestream: reading a top-level file or folder: %w", unexpected(err))
	}
	topKind := kind(start[0])
	if topKind != fileEntry && topKind != folderEntry {
		return TopEntry{}, fmt.Errorf("%w: a top-level entry of kind %d", ErrMalformed, start[0])
	}
	nameBytes := int(binary.BigEndian.Uint16(start[1:]))
	if nameBytes >= pathBytesLimit {
		return TopEntry{}, fmt.Errorf("%w: a path of %d bytes", ErrPathTooLong, nameBytes)
	}
	name := make([]byte, nameBytes)
	if _, err := io.ReadFull(r, name); err != nil {
		return TopEntry{}, fmt.Errorf("filestream: reading a top-level file or folder: %w", unexpected(err))
	}
	if err := wellFormedName(string(name)); err != nil {
		return TopEntry{}, err
	}
	if strings.Contains(string(name), "/") {
		return TopEntry{}, fmt.Errorf("%w: the top-level name %q holds a folder", ErrMalformed, name)
	}
	return TopEntry{Name: string(name), Folder: topKind == folderEntry}, nil
}

type topsExpected struct {
	tops []TopEntry
	next int
}

func (e *topsExpected) arrived(name string, folder bool) error {
	if e == nil || path.Dir(name) != "." {
		return nil
	}
	if e.next >= len(e.tops) || e.tops[e.next] != (TopEntry{Name: name, Folder: folder}) {
		return fmt.Errorf("%w: %q is not the top-level file or folder announced next", ErrMalformed, name)
	}
	e.next++
	return nil
}

func (e *topsExpected) allArrived() error {
	if e == nil || e.next == len(e.tops) {
		return nil
	}
	return fmt.Errorf("%w: %d of the %d top-level files and folders announced arrived", ErrMalformed, e.next, len(e.tops))
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
	if nameBytes >= pathBytesLimit {
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
