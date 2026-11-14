package status

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	helperDirectory = "LANHOP_STATUS_TEST_DIRECTORY"
	helperHolds     = "LANHOP_STATUS_TEST_HOLDS"
)

func writerInAnotherProcess(t *testing.T, directory string, holds bool) func() {
	t.Helper()
	helper := exec.Command(os.Args[0], "-test.run=^TestHelperWritesAStatus$")
	helper.Env = append(os.Environ(), helperDirectory+"="+directory)
	if !holds {
		if output, err := helper.CombinedOutput(); err != nil {
			t.Fatalf("the helper failed: %v\n%s", err, output)
		}
		return func() {}
	}
	helper.Env = append(helper.Env, helperHolds+"=1")
	release, err := helper.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); !Running(directory); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the helper never took the status")
		}
	}
	return func() {
		release.Close()
		helper.Wait()
	}
}

func TestReportReadsBackAsItWasWritten(t *testing.T) {
	directory := t.TempDir()
	written := Report{State: Sharing, Machines: []string{"desk", "laptop"}, Address: "192.168.1.9:24745", Error: "none"}
	if err := OwnerOf(directory).Write(written); err != nil {
		t.Fatalf("Write: %v", err)
	}
	read, err := Read(directory)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if read.State != written.State || !slices.Equal(read.Machines, written.Machines) || read.Address != written.Address || read.Error != written.Error {
		t.Errorf("read %+v, want %+v", read, written)
	}
}

func TestErrorOverSeveralLinesIsKeptOnOne(t *testing.T) {
	directory := t.TempDir()
	if err := OwnerOf(directory).Write(Report{State: Failing, Error: "first line\nsecond\tline\n"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	read, err := Read(directory)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if read.Error != "first line second line" || read.State != Failing {
		t.Errorf("read %+v, want the error on one line and the state kept", read)
	}
}

func TestNewReportReplacesTheOldOneAndLeavesOnlyTheStatusAndItsLock(t *testing.T) {
	directory := t.TempDir()
	owner := OwnerOf(directory)
	if err := owner.Write(Report{State: Connecting, Machines: []string{"desk"}, Error: "old"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := owner.Write(Report{State: Connected, Machines: []string{"desk"}}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	read, err := Read(directory)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if read.State != Connected || read.Error != "" {
		t.Errorf("read %+v, want only the second report", read)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if !slices.Equal(names, []string{FileName, lockFileName}) {
		t.Errorf("the directory holds %v, want only %s and %s", names, FileName, lockFileName)
	}
}

func TestOwnerCreatesTheDirectoryItWritesIn(t *testing.T) {
	directory := t.TempDir() + "/lanhop"
	if err := OwnerOf(directory).Write(Report{State: Accessibility}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if read, err := Read(directory); err != nil || read.State != Accessibility {
		t.Errorf("Read = %+v, %v, want the report", read, err)
	}
}

func TestNothingRunsWhereNoOwnerEverWrote(t *testing.T) {
	if Running(t.TempDir()) {
		t.Error("Running = true in a directory without a status")
	}
}

func TestTheOwnerRunsWhileItHoldsTheStatus(t *testing.T) {
	directory := t.TempDir()
	owner := OwnerOf(directory)
	if err := owner.Write(Report{State: Waiting}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !owner.Owns() || !Running(directory) {
		t.Errorf("Owns = %v, Running = %v, want both true", owner.Owns(), Running(directory))
	}
}

func TestWritersOfOneProcessShareTheStatus(t *testing.T) {
	directory := t.TempDir()
	if err := OwnerOf(directory).Write(Report{State: Accessibility}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := OwnerOf(directory).Write(Report{State: Waiting}); err != nil {
		t.Fatalf("a second writer of the same process cannot write: %v", err)
	}
	if read, err := Read(directory); err != nil || read.State != Waiting {
		t.Errorf("Read = %+v, %v, want the report of the second writer", read, err)
	}
}

func TestAWriterInAnotherProcessLeavesTheStatusOfTheFirstAlone(t *testing.T) {
	directory := t.TempDir()
	release := writerInAnotherProcess(t, directory, true)
	defer release()
	second := OwnerOf(directory)
	if err := second.Write(Report{State: Stopped, Error: "address already in use"}); err == nil {
		t.Error("the second writer wrote while the first one holds the status")
	}
	if second.Owns() {
		t.Error("the second writer owns the status")
	}
	if read, err := Read(directory); err != nil || read.State != Connected {
		t.Errorf("Read = %+v, %v, want the report of the first writer", read, err)
	}
}

func TestAWriterThatEndedNoLongerRuns(t *testing.T) {
	directory := t.TempDir()
	writerInAnotherProcess(t, directory, false)
	if read, err := Read(directory); err != nil || read.State != Connected {
		t.Fatalf("Read = %+v, %v, want what the helper wrote", read, err)
	}
	if Running(directory) {
		t.Error("Running = true after the writer ended")
	}
	if err := OwnerOf(directory).Write(Report{State: Waiting}); err != nil {
		t.Errorf("a new writer cannot take over after the old one ended: %v", err)
	}
}

func TestHelperWritesAStatus(t *testing.T) {
	directory := os.Getenv(helperDirectory)
	if directory == "" {
		t.Skip("only runs as the helper of another test")
	}
	if err := OwnerOf(directory).Write(Report{State: Connected, Machines: []string{"desk"}}); err != nil {
		t.Fatal(err)
	}
	if os.Getenv(helperHolds) != "" {
		io.ReadAll(os.Stdin)
	}
}

func TestDeparturesReadBackWithTheirTimes(t *testing.T) {
	directory := t.TempDir()
	left := []Departure{
		{Name: "laptop", At: time.Date(2026, 11, 13, 14, 2, 0, 0, time.UTC)},
		{Name: "tablet", At: time.Date(2026, 11, 12, 9, 30, 0, 0, time.FixedZone("BRT", -3*60*60))},
	}
	if err := OwnerOf(directory).Write(Report{State: Waiting, Left: left}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	read, err := Read(directory)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(read.Left) != len(left) {
		t.Fatalf("read %+v, want %+v", read.Left, left)
	}
	for i := range left {
		if read.Left[i].Name != left[i].Name || !read.Left[i].At.Equal(left[i].At) {
			t.Errorf("departure %d read as %+v, want %+v", i, read.Left[i], left[i])
		}
	}
}

func TestSeatsReadBackWithTheirVersionAndMark(t *testing.T) {
	directory := t.TempDir()
	seats := []Seat{{Name: "office-mac", Version: "v0.1.3", Mark: "follows"}, {Name: "old", Mark: "needs the installer"}}
	if err := OwnerOf(directory).Write(Report{State: Sharing, Machines: []string{"office-mac", "old"}, Seats: seats}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	read, err := Read(directory)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !slices.Equal(read.Seats, seats) {
		t.Errorf("seats read as %+v, want %+v", read.Seats, seats)
	}
}

func TestWhatADraggedFileCannotDoReadsBack(t *testing.T) {
	directory := t.TempDir()
	written := Report{State: Sharing, Machines: []string{"laptop", "wayland box"}, DragCannotReach: []string{"laptop", "wayland box"}, DragCannotLeave: true}
	if err := OwnerOf(directory).Write(written); err != nil {
		t.Fatalf("Write: %v", err)
	}
	read, err := Read(directory)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !slices.Equal(read.DragCannotReach, written.DragCannotReach) || !read.DragCannotLeave {
		t.Errorf("read %+v, want the machines a dragged file cannot reach and that it cannot leave this one", read)
	}
}

func TestAReportWithNothingAboutDraggedFilesReadsBackWithNothing(t *testing.T) {
	directory := t.TempDir()
	if err := OwnerOf(directory).Write(Report{State: Waiting}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	read, err := Read(directory)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(read.DragCannotReach) != 0 || read.DragCannotLeave {
		t.Errorf("read %+v, want nothing about dragged files", read)
	}
	contents, err := os.ReadFile(filepath.Join(directory, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "drag") {
		t.Errorf("the status holds %q, want no line about dragged files", contents)
	}
}

func TestOnlyYesSaysThatADraggedFileCannotLeaveAndAnEmptyNameIsNoMachine(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, FileName), []byte("state=sharing\ndrag-cannot-leave=no\ndrag-cannot-reach=\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	read, err := Read(directory)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if read.DragCannotLeave || len(read.DragCannotReach) != 0 {
		t.Errorf("read %+v, want neither a value other than yes nor an empty name to count", read)
	}
}
