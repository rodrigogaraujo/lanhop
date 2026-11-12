package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/layout"
	"github.com/rodrigogaraujo/lanhop/internal/pairing"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

const thisMachine = "this-machine"

var (
	deskNextDoor   = trust.Peer{Name: "desk-next-door", Fingerprint: someFingerprint(11)}
	laptopNextDoor = trust.Peer{Name: "laptop-next-door", Fingerprint: someFingerprint(12)}
	firstSave      = time.Date(2026, time.November, 12, 9, 0, 0, 0, time.UTC)
)

type layoutFile struct {
	path  string
	saves int
}

func layoutFileIn(directory string) *layoutFile {
	return &layoutFile{path: filepath.Join(directory, layoutFileName)}
}

func (f *layoutFile) save(t *testing.T, drawing string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(f.path, []byte(drawing), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	f.saves++
	saved := firstSave.Add(time.Duration(f.saves) * time.Minute)
	if err := os.Chtimes(f.path, saved, saved); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
}

func (f *layoutFile) remove(t *testing.T) {
	t.Helper()
	if err := os.Remove(f.path); err != nil {
		t.Fatalf("Remove: %v", err)
	}
}

func deskFollowing(t *testing.T, ownScreenHasEdges bool) (*deskOnDisk, *layoutFile, *screen) {
	t.Helper()
	directory := useConfigDirectory(t)
	file := layoutFileIn(directory)
	output := &screen{}
	peersPath := filepath.Join(directory, peersFileName)
	return &deskOnDisk{
		path:      file.path,
		peersPath: peersPath,
		own:       thisMachine,
		known:     knownOnThisDesk(thisMachine, peersOnDisk(peersPath)),
		ownEdges:  ownScreenHasEdges,
		stdout:    output,
	}, file, output
}

func expectNeighbour(t *testing.T, desk *layout.Layout, side layout.Side, want string) {
	t.Helper()
	if desk == nil {
		t.Fatalf("no layout is in use, want one with %q on the %s of this machine", want, side)
	}
	if got, _ := desk.Neighbour(thisMachine, side); got != want {
		t.Errorf("the layout in use has %q on the %s of this machine, want %q", got, side, want)
	}
}

func expectShown(t *testing.T, output *screen, wanted ...string) {
	t.Helper()
	for _, want := range wanted {
		if !strings.Contains(output.String(), want) {
			t.Errorf("lanhop printed %q, which lacks %q", output.String(), want)
		}
	}
}

func expectNotShown(t *testing.T, output *screen, unwanted ...string) {
	t.Helper()
	for _, text := range unwanted {
		if strings.Contains(output.String(), text) {
			t.Errorf("lanhop printed %q, which must not say %q", output.String(), text)
		}
	}
}

func TestLayoutFileThatAppearsIsUsedAndFollowedAsItChanges(t *testing.T) {
	desk, file, output := deskFollowing(t, true)
	trustAlready(t, filepath.Dir(file.path), deskNextDoor)

	desk.start()
	if desk.Layout() != nil {
		t.Fatal("a layout is in use although its file does not exist")
	}
	expectShown(t, output, "Edge switching is off until a layout exists", file.path, "The hotkeys work without it")

	file.save(t, "this-machine desk-next-door\n")
	desk.refresh()
	expectNeighbour(t, desk.Layout(), layout.Right, deskNextDoor.Name)
	expectShown(t, output, "Edge switching is on, with the layout in "+file.path)

	file.save(t, "desk-next-door this-machine\n")
	desk.refresh()
	expectNeighbour(t, desk.Layout(), layout.Left, deskNextDoor.Name)
	expectNeighbour(t, desk.Layout(), layout.Right, "")
}

func TestLayoutFileWithProblemsNeverReplacesTheLayoutThatWorksAndItsProblemsAreSaidOnce(t *testing.T) {
	desk, file, output := deskFollowing(t, true)
	trustAlready(t, filepath.Dir(file.path), deskNextDoor)
	file.save(t, "this-machine desk-next-door\n")
	desk.start()

	file.save(t, "# moved the desk\nthis-machine stranger\ndesk-next-door this-machine\n")
	desk.refresh()
	desk.refresh()
	desk.refresh()

	expectNeighbour(t, desk.Layout(), layout.Right, deskNextDoor.Name)
	expectShown(t, output,
		"The layout in "+file.path+" has problems:",
		`  line 2: "stranger" is not a known machine`,
		`  line 3: "this-machine" is repeated, first seen on line 2`,
		"lanhop keeps the layout it had.",
	)
	if said := strings.Count(output.String(), "has problems"); said != 1 {
		t.Errorf("the problems of one save were said %d times, want once: %q", said, output.String())
	}

	file.save(t, "desk-next-door this-machine\n")
	desk.refresh()
	expectNeighbour(t, desk.Layout(), layout.Left, deskNextDoor.Name)

	file.save(t, ". .\n")
	desk.refresh()
	expectNeighbour(t, desk.Layout(), layout.Left, deskNextDoor.Name)
	if said := strings.Count(output.String(), "has problems"); said != 2 {
		t.Errorf("after a second save with problems they were said %d times in all, want 2: %q", said, output.String())
	}
}

func TestLayoutFileWithProblemsAtStartLeavesEdgeSwitchingOffAndSaysWhy(t *testing.T) {
	desk, file, output := deskFollowing(t, true)
	file.save(t, "this-machine desk-next-door\n")

	desk.start()
	desk.refresh()

	if desk.Layout() != nil {
		t.Error("a layout that names a machine that is not paired is in use")
	}
	expectShown(t, output,
		"The layout in "+file.path+" has problems:",
		`  line 1: "desk-next-door" is not a known machine`,
		"Edge switching is off until they are fixed. The hotkeys work without it.",
	)
	expectNotShown(t, output, "until a layout exists", "keeps the layout it had", "Edge switching is on")
}

func TestLayoutIsReadAgainWhenTheMachineItNamesIsPaired(t *testing.T) {
	desk, file, output := deskFollowing(t, true)
	file.save(t, "this-machine desk-next-door\n")
	desk.start()

	trustAlready(t, filepath.Dir(file.path), deskNextDoor)
	desk.refresh()

	expectNeighbour(t, desk.Layout(), layout.Right, deskNextDoor.Name)
	expectShown(t, output, "Edge switching is on")
}

func TestLayoutFileThatIsRemovedTurnsEdgeSwitchingOff(t *testing.T) {
	desk, file, output := deskFollowing(t, true)
	trustAlready(t, filepath.Dir(file.path), deskNextDoor)
	file.save(t, "this-machine desk-next-door\n")
	desk.start()
	expectNeighbour(t, desk.Layout(), layout.Right, deskNextDoor.Name)

	file.remove(t)
	desk.refresh()
	desk.refresh()

	if desk.Layout() != nil {
		t.Error("a layout is still in use after its file was removed")
	}
	expectShown(t, output, "The layout file "+file.path+" is gone. Edge switching is off until a layout exists.")
	if said := strings.Count(output.String(), "is gone"); said != 1 {
		t.Errorf("the removal was said %d times, want once", said)
	}

	file.save(t, "desk-next-door this-machine\n")
	desk.refresh()
	expectNeighbour(t, desk.Layout(), layout.Left, deskNextDoor.Name)
}

func TestLayoutSavedAgainWithTheSameDeskIsNotAnnouncedAgain(t *testing.T) {
	desk, file, output := deskFollowing(t, true)
	trustAlready(t, filepath.Dir(file.path), deskNextDoor)
	file.save(t, "this-machine desk-next-door\n")
	desk.start()

	file.save(t, "# the same desk\nthis-machine    desk-next-door\n")
	desk.refresh()

	if said := strings.Count(output.String(), "Edge switching is on"); said != 1 {
		t.Errorf("the same desk was announced %d times, want once: %q", said, output.String())
	}
}

func TestLayoutInUseSaysWhetherTheEdgesOfThisMachineCross(t *testing.T) {
	tests := []struct {
		name              string
		ownScreenHasEdges bool
		drawing           string
		want              []string
		unwanted          []string
	}{
		{
			name:              "a machine that finds its cursor and is in the layout",
			ownScreenHasEdges: true,
			drawing:           "this-machine desk-next-door\n",
			want:              []string{"Edge switching is on, with the layout in", "Push the cursor through an edge of a screen"},
			unwanted:          []string{"do not cross"},
		},
		{
			name:              "a machine that cannot find its cursor",
			ownScreenHasEdges: false,
			drawing:           "this-machine desk-next-door\n",
			want: []string{
				"Edge switching is on between the machines this one controls",
				"The edges of this machine's own screen do not cross, because lanhop cannot find the cursor of this machine on this system yet.",
				"Leave this machine with a hotkey.",
			},
		},
		{
			name:              "a machine that the layout does not name",
			ownScreenHasEdges: true,
			drawing:           "desk-next-door\n",
			want: []string{
				"Edge switching is on between the machines this one controls",
				`The edges of this machine's own screen do not cross, because the layout does not name this machine, "this-machine".`,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			desk, file, output := deskFollowing(t, test.ownScreenHasEdges)
			trustAlready(t, filepath.Dir(file.path), deskNextDoor)
			file.save(t, test.drawing)

			desk.start()

			expectShown(t, output, append(test.want, file.path)...)
			expectNotShown(t, output, append(test.unwanted, "Edge switching is off")...)
		})
	}
}

func runLayout(t *testing.T, args ...string) (exitCode int, stdout, stderr string) {
	t.Helper()
	var out, problems bytes.Buffer
	exitCode = run(append([]string{"layout"}, args...), nobodyTyping(), &out, &problems)
	return exitCode, out.String(), problems.String()
}

func TestLayoutCommandPrintsTheDeskAsLanhopUnderstoodItWithThisMachineMarked(t *testing.T) {
	directory := useConfigDirectory(t)
	trustAlready(t, directory, deskNextDoor, laptopNextDoor)
	file := layoutFileIn(directory)
	own := pairing.OwnName()
	file.save(t, fmt.Sprintf("# my desk\n%s   %s\n.  %s\n", deskNextDoor.Name, own, laptopNextDoor.Name))

	exitCode, stdout, stderr := runLayout(t)

	if exitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr)
	}
	want := fmt.Sprintf("The layout file is %s\n\n", file.path) +
		fmt.Sprintf("%-16s%s\n", deskNextDoor.Name, own) +
		fmt.Sprintf("%-16s%s\n", ".", laptopNextDoor.Name) +
		"\n" +
		fmt.Sprintf("%s: right %s\n", deskNextDoor.Name, own) +
		fmt.Sprintf("%s (this machine): left %s, below %s\n", own, deskNextDoor.Name, laptopNextDoor.Name) +
		fmt.Sprintf("%s: above %s\n", laptopNextDoor.Name, own)
	if stdout != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", stdout, want)
	}
}

func TestLayoutCommandSaysWhenTheLayoutDoesNotNameThisMachine(t *testing.T) {
	directory := useConfigDirectory(t)
	trustAlready(t, directory, deskNextDoor, laptopNextDoor)
	layoutFileIn(directory).save(t, "desk-next-door laptop-next-door\n")

	exitCode, stdout, stderr := runLayout(t)

	if exitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr)
	}
	for _, want := range []string{
		"desk-next-door: right laptop-next-door\n",
		fmt.Sprintf("This machine, %q, is not in the layout", pairing.OwnName()),
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, which lacks %q", stdout, want)
		}
	}
	if strings.Contains(stdout, "(this machine)") {
		t.Errorf("stdout = %q, which marks a machine as this one", stdout)
	}
}

func TestLayoutCommandPrintsEachProblemWithItsLineAndFails(t *testing.T) {
	directory := useConfigDirectory(t)
	trustAlready(t, directory, deskNextDoor)
	file := layoutFileIn(directory)
	file.save(t, "desk-next-door stranger\n\ndesk-next-door\n")

	exitCode, stdout, stderr := runLayout(t)

	if exitCode != 1 {
		t.Errorf("exit code = %d, want 1", exitCode)
	}
	if want := fmt.Sprintf("The layout file is %s\n\n", file.path); stdout != want {
		t.Errorf("stdout = %q, want only %q", stdout, want)
	}
	for _, want := range []string{
		"the layout has problems:",
		`  line 1: "stranger" is not a known machine`,
		`  line 3: "desk-next-door" is repeated, first seen on line 1`,
		fmt.Sprintf("This machine is %q.", pairing.OwnName()),
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, which lacks %q", stderr, want)
		}
	}
}

func TestLayoutCommandWithoutAFileSaysWhereItGoesAndShowsAnExampleOfThreeLines(t *testing.T) {
	directory := useConfigDirectory(t)

	exitCode, stdout, stderr := runLayout(t)

	if exitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr)
	}
	for _, want := range []string{
		"The layout file is " + filepath.Join(directory, layoutFileName) + "\n",
		"There is no layout file yet.",
		"  # my desk\n  laptop   desktop   macbook\n  .        server    .\n",
		fmt.Sprintf("This machine is %q.", pairing.OwnName()),
		`"lanhop peers" lists the names of the others.`,
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, which lacks %q", stdout, want)
		}
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Errorf("the config directory was created: %v", err)
	}
}

func TestLayoutCommandFailsOnADamagedListOfTrustedMachinesInsteadOfCallingEveryNameUnknown(t *testing.T) {
	directory := useConfigDirectory(t)
	layoutFileIn(directory).save(t, "desk-next-door\n")
	if err := os.WriteFile(filepath.Join(directory, peersFileName), []byte("{"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	exitCode, _, stderr := runLayout(t)

	if exitCode != 1 || !strings.Contains(stderr, "is damaged") || strings.Contains(stderr, "not a known machine") {
		t.Errorf("exit code = %d, stderr = %q, want 1 and only the damaged list of trusted machines", exitCode, stderr)
	}
}

func TestLayoutCommandWithAnArgumentShowsUsage(t *testing.T) {
	useConfigDirectory(t)

	exitCode, _, stderr := runLayout(t, "desk")

	if exitCode != 2 || !strings.Contains(stderr, "layout takes no argument") || !strings.Contains(stderr, "Usage:") {
		t.Errorf("exit code = %d, stderr = %q, want 2 and the usage", exitCode, stderr)
	}
}

func TestHelpNamesTheLayoutCommandAndSaysWhatTheLayoutFileIs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"help"}, nobodyTyping(), &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	for _, mention := range []string{"lanhop layout ", "The layout file draws the desk", "a dot for an empty place"} {
		if !strings.Contains(stdout.String(), mention) {
			t.Errorf("help does not mention %q", mention)
		}
	}
}
