//go:build darwin && cgo

package macos

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
)

func pasteboardOfThisTest(t *testing.T) Pasteboard {
	t.Helper()
	p := Pasteboard{name: fmt.Sprintf("com.github.rodrigogaraujo.lanhop.test.%d", time.Now().UnixNano())}
	t.Cleanup(p.release)
	return p
}

func TestTextWrittenToThePasteboardReadsBackTheSame(t *testing.T) {
	texts := map[string]string{
		"a word":                 "hello",
		"accents, emoji, kanji":  "olá, ação 🙂👍🏽 日本語",
		"tabs and several lines": "line one\r\nline two\n\tindented\n",
		"an empty text":          "",
		"64 KiB":                 strings.Repeat("x", 64<<10),
		"the limit":              strings.Repeat("y", clipboard.Limit),
	}
	for name, text := range texts {
		t.Run(name, func(t *testing.T) {
			p := pasteboardOfThisTest(t)
			if _, err := p.Write(text); err != nil {
				t.Fatalf("Write: %v", err)
			}

			got, err := p.Read()

			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if got != text {
				t.Errorf("read %d bytes back, want the same %d bytes", len(got), len(text))
			}
		})
	}
}

func TestEachWriteMovesTheMarkAndReadingDoesNot(t *testing.T) {
	p := pasteboardOfThisTest(t)
	before := p.Mark()

	first, err := p.Write("first")
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if first == before {
		t.Errorf("the mark stayed at %d after a write", before)
	}
	if got := p.Mark(); got != first {
		t.Errorf("Mark = %d, want the %d that Write returned", got, first)
	}
	if _, err := p.Read(); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got := p.Mark(); got != first {
		t.Errorf("Mark after a read = %d, want %d", got, first)
	}

	second, err := p.Write("second")
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if second == first {
		t.Errorf("the mark stayed at %d after a second write", first)
	}
}

func TestAPasteboardWithoutTextIsNotText(t *testing.T) {
	p := pasteboardOfThisTest(t)

	if _, err := p.Read(); !errors.Is(err, clipboard.ErrNotText) {
		t.Errorf("Read of a pasteboard with nothing on it: error = %v, want %v", err, clipboard.ErrNotText)
	}
}

func TestACopyThatAPasswordManagerMarkedPrivateIsNotRead(t *testing.T) {
	p := pasteboardOfThisTest(t)
	if _, err := p.write("a password", true); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := p.Read()

	if !errors.Is(err, clipboard.ErrPrivate) || got != "" {
		t.Errorf("Read = %q, %v, want nothing and %v", got, err, clipboard.ErrPrivate)
	}
}

func TestATextOverTheLimitIsNotRead(t *testing.T) {
	p := pasteboardOfThisTest(t)
	if _, err := p.Write(strings.Repeat("z", clipboard.Limit+1)); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if _, err := p.Read(); !errors.Is(err, clipboard.ErrTooLarge) {
		t.Errorf("Read: error = %v, want %v", err, clipboard.ErrTooLarge)
	}
}

func TestWritingAndReading64KiBTakesLittleOfTheTimeACopyMayTake(t *testing.T) {
	const budget = 300 * time.Millisecond
	p := pasteboardOfThisTest(t)
	text := strings.Repeat("w", 64<<10)

	start := time.Now()
	if _, err := p.Write(text); err != nil {
		t.Fatalf("Write: %v", err)
	}
	wrote := time.Since(start)
	if _, err := p.Read(); err != nil {
		t.Fatalf("Read: %v", err)
	}
	both := time.Since(start)

	t.Logf("64 KiB: write %v, read %v", wrote, both-wrote)
	if both > budget/3 {
		t.Errorf("writing and reading 64 KiB took %v, more than a third of the %v that SC-001 allows a whole copy", both, budget)
	}
}

func TestTheGeneralPasteboardGivesAMarkWithoutReadingIt(t *testing.T) {
	if mark := GeneralPasteboard().Mark(); mark < 0 {
		t.Errorf("Mark of the general pasteboard = %d", mark)
	}
}
