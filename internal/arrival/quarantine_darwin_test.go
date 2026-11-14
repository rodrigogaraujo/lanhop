//go:build darwin && cgo

package arrival

import (
	"path/filepath"
	"testing"

	"github.com/rodrigogaraujo/lanhop/internal/macos"
)

func TestEveryFileAndFolderThatArrivesCarriesTheQuarantineMark(t *testing.T) {
	f, clock, _ := openFolder(t, 0, 0)
	a, _ := arrive(t, f, 1,
		aFolder("photos"),
		aFile("photos/beach.jpg", "sand and sea"),
		aFile("docs/deep/plan.md", "a plan in folders that were not announced"),
		aFile("notes.txt", "remember the milk"),
	)
	want := quarantineValue(clock.now())
	for _, relative := range []string{"photos", "photos/beach.jpg", "docs", "docs/deep", "docs/deep/plan.md", "notes.txt"} {
		if got, err := macos.Quarantine(filepath.Join(a.path, relative)); err != nil || got != want {
			t.Errorf("the quarantine mark of %s is %q (%v), want %q", relative, got, err, want)
		}
	}
}
