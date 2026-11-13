package status

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

func TestReportReadsBackAsItWasWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	written := Report{PID: 4242, State: Sharing, Machines: []string{"desk", "laptop"}, Address: "192.168.1.9:24745", Error: "none"}
	if err := Write(path, written); err != nil {
		t.Fatalf("Write: %v", err)
	}
	read, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if read.PID != written.PID || read.State != written.State || !slices.Equal(read.Machines, written.Machines) || read.Address != written.Address || read.Error != written.Error {
		t.Errorf("read %+v, want %+v", read, written)
	}
}

func TestErrorOverSeveralLinesIsKeptOnOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := Write(path, Report{State: Failing, Error: "first line\nsecond\tline\n"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	read, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if read.Error != "first line second line" || read.State != Failing {
		t.Errorf("read %+v, want the error on one line and the state kept", read)
	}
}

func TestNewReportReplacesTheOldOneAndLeavesNoOtherFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, FileName)
	if err := Write(path, Report{State: Connecting, Machines: []string{"desk"}, Error: "old"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := Write(path, Report{State: Connected, Machines: []string{"desk"}}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	read, err := Read(path)
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
	if len(entries) != 1 || entries[0].Name() != FileName {
		t.Errorf("the directory holds %v, want only %s", entries, FileName)
	}
}

func TestThisProcessIsRunning(t *testing.T) {
	if !Running(os.Getpid()) {
		t.Error("Running(own pid) = false")
	}
}

func TestNoProcessIsRunningWithoutAPID(t *testing.T) {
	if Running(0) || Running(-1) {
		t.Error("a process without a pid counts as running")
	}
}

func TestProcessThatEndedIsNotRunning(t *testing.T) {
	ended := exec.Command(os.Args[0], "-test.run=^$")
	if err := ended.Run(); err != nil {
		t.Fatalf("run a process that ends: %v", err)
	}
	if Running(ended.Process.Pid) {
		t.Errorf("Running(%d) = true for a process that ended", ended.Process.Pid)
	}
}
