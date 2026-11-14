package cli

import (
	"flag"
	"io"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestAFileMayComeBeforeOrAfterTheFlags(t *testing.T) {
	for _, args := range [][]string{
		{"-now", "/tmp/a.txt", "-v"},
		{"/tmp/a.txt", "-now", "-v"},
		{"-now", "-v", "/tmp/a.txt"},
	} {
		set := flag.NewFlagSet("drop", flag.ContinueOnError)
		set.SetOutput(io.Discard)
		now := set.Bool("now", false, "")
		verbose := set.Bool("v", false, "")

		positional, err := ParseInterleaved(set, args)

		if err != nil || !slices.Equal(positional, []string{"/tmp/a.txt"}) || !*now || !*verbose {
			t.Errorf("%v: positional %v, now %v, verbose %v, %v", args, positional, *now, *verbose, err)
		}
	}
}

func TestAnUnknownFlagIsAnError(t *testing.T) {
	set := flag.NewFlagSet("watch", flag.ContinueOnError)
	set.SetOutput(io.Discard)

	if _, err := ParseInterleaved(set, []string{"file", "-nope"}); err == nil {
		t.Error("an unknown flag after the file was accepted")
	}
}

func TestEachLineCarriesTheTimeAndLaterLinesAreIndented(t *testing.T) {
	var out strings.Builder
	start := time.Date(2026, 11, 13, 10, 0, 0, 0, time.Local)
	p := &Printer{out: &out, start: start, now: func() time.Time { return start.Add(1500 * time.Microsecond) }}

	p.Say("types: %d\nfirst\nsecond\n", 2)

	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 3 || lines[0] != "10:00:00.001       +1.5 ms  types: 2" {
		t.Fatalf("printed %q", out.String())
	}
	if strings.TrimSpace(lines[1]) != "first" || len(lines[1]) != len(lines[0])-len("types: 2")+len("first") {
		t.Errorf("second line %q is not indented under the first", lines[1])
	}
}
