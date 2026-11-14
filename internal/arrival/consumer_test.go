package arrival

import (
	"errors"
	"io"
	"path/filepath"
	"slices"
	"testing"
)

type incomingFileAsTheProtocolWants interface {
	io.Writer
	Whole() error
}

type arrivalsAsTheProtocolWants interface {
	Expect(bytes int64, filesAndFolders int) error
	Folder(name string) error
	File(name string, size int64) (incomingFileAsTheProtocolWants, error)
	Finish() error
	GiveUp()
}

type adapted struct{ *Arrival }

func (a adapted) File(name string, size int64) (incomingFileAsTheProtocolWants, error) {
	file, err := a.Arrival.File(name, size)
	if err != nil {
		return nil, err
	}
	return file, nil
}

var _ arrivalsAsTheProtocolWants = adapted{}

func TestAnArrivalServesTheContentProtocolThroughAThreeLineAdapter(t *testing.T) {
	f, _, _ := openFolder(t, 0, 0)
	a := f.Expecting(5)
	var arrivals arrivalsAsTheProtocolWants = adapted{a}
	if err := arrivals.Expect(9, 3); err != nil {
		t.Fatalf("Expect: %v", err)
	}
	if err := arrivals.Folder("photos"); err != nil {
		t.Fatalf("Folder: %v", err)
	}
	file, err := arrivals.File("photos/beach.jpg", 4)
	if err != nil {
		t.Fatalf("File: %v", err)
	}
	if _, err := io.WriteString(file, "sand"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := file.Whole(); err != nil {
		t.Fatalf("Whole: %v", err)
	}
	notes, err := arrivals.File("notes.txt", 5)
	if err != nil {
		t.Fatalf("File: %v", err)
	}
	if _, err := io.WriteString(notes, "milk!"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := notes.Whole(); err != nil {
		t.Fatalf("Whole: %v", err)
	}
	if err := arrivals.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	want := []string{filepath.Join(a.path, "photos"), filepath.Join(a.path, "notes.txt")}
	if paths := a.Paths(); !slices.Equal(paths, want) {
		t.Errorf("Paths = %q, want %q", paths, want)
	}
	expectContent(t, want[1], "milk!")

	refused, err := adapted{f.Expecting(6)}.File("early.txt", 1)
	if !errors.Is(err, ErrOutOfOrder) || refused != nil {
		t.Errorf("File before Expect through the adapter = %v, %v, want a nil file and ErrOutOfOrder", refused, err)
	}
}
