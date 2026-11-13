package status

import (
	"os"
	"os/exec"
	"slices"
	"testing"
)

const helperDirectory = "LANHOP_STATUS_TEST_DIRECTORY"

func TestReportReadsBackAsItWasWritten(t *testing.T) {
	directory := t.TempDir()
	written := Report{State: Sharing, Machines: []string{"desk", "laptop"}, Address: "192.168.1.9:24745", Error: "none"}
	if err := NewOwner(directory).Write(written); err != nil {
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
	if err := NewOwner(directory).Write(Report{State: Failing, Error: "first line\nsecond\tline\n"}); err != nil {
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
	owner := NewOwner(directory)
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
	if err := NewOwner(directory).Write(Report{State: Accessibility}); err != nil {
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
	owner := NewOwner(directory)
	if err := owner.Write(Report{State: Waiting}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !owner.Owns() || !Running(directory) {
		t.Errorf("Owns = %v, Running = %v, want both true", owner.Owns(), Running(directory))
	}
}

func TestASecondWriterLeavesTheStatusOfTheFirstAlone(t *testing.T) {
	directory := t.TempDir()
	if err := NewOwner(directory).Write(Report{State: Connected, Machines: []string{"desk"}}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	second := NewOwner(directory)
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
	helper := exec.Command(os.Args[0], "-test.run=^TestHelperWritesAStatusAndEnds$")
	helper.Env = append(os.Environ(), helperDirectory+"="+directory)
	if output, err := helper.CombinedOutput(); err != nil {
		t.Fatalf("the helper failed: %v\n%s", err, output)
	}
	if read, err := Read(directory); err != nil || read.State != Connected {
		t.Fatalf("Read = %+v, %v, want what the helper wrote", read, err)
	}
	if Running(directory) {
		t.Error("Running = true after the writer ended")
	}
	if err := NewOwner(directory).Write(Report{State: Waiting}); err != nil {
		t.Errorf("a new writer cannot take over after the old one ended: %v", err)
	}
}

func TestHelperWritesAStatusAndEnds(t *testing.T) {
	directory := os.Getenv(helperDirectory)
	if directory == "" {
		t.Skip("only runs as the helper of another test")
	}
	if err := NewOwner(directory).Write(Report{State: Connected, Machines: []string{"desk"}}); err != nil {
		t.Fatal(err)
	}
}
