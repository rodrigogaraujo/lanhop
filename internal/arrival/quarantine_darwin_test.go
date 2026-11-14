//go:build darwin && cgo

package arrival

import (
	"path/filepath"
	"testing"

	"github.com/rodrigogaraujo/lanhop/internal/macos"
)

func TestEveryFileAndFolderThatArrivesCarriesTheQuarantineMark(t *testing.T) {
	f, clock, _ := openFolder(t, 0, 0)
	entries := []sent{
		aFolder("photos"),
		aFile("photos/beach.jpg", "sand and sea"),
		aFolder("docs"),
		aFolder("docs/deep"),
		aFile("docs/deep/plan.md", "a plan two folders down"),
		aFile("notes.txt", "remember the milk"),
	}
	a, _ := arrive(t, f, 1, entries...)
	want := quarantineValue(clock.now())
	for _, entry := range entries {
		if got, err := macos.Quarantine(filepath.Join(a.path, entry.name)); err != nil || got != want {
			t.Errorf("the quarantine mark of %s is %q (%v), want %q", entry.name, got, err, want)
		}
	}
}

func TestAFileCarriesTheQuarantineMarkBeforeItIsWhole(t *testing.T) {
	f, clock, _ := openFolder(t, 0, 0)
	a := expectingWith(t, f, 1, 4, 1)
	file, err := a.File("plan.md", 4)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := macos.Quarantine(file.temporary); err != nil || got != quarantineValue(clock.now()) {
		t.Errorf("the temporary file carries the mark %q (%v), want %q", got, err, quarantineValue(clock.now()))
	}
	a.GiveUp()
}
