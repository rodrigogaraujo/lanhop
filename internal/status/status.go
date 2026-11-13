package status

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	FileName     = "status"
	lockFileName = "status.lock"
)

type State string

const (
	Accessibility State = "accessibility"
	Connecting    State = "connecting"
	Connected     State = "connected"
	NotReached    State = "not-reached"
	Failing       State = "failing"
	Waiting       State = "waiting"
	Sharing       State = "sharing"
	Stopped       State = "stopped"
)

type Report struct {
	State    State
	Machines []string
	Address  string
	Error    string
}

type Owner struct {
	directory string
	lock      *os.File
}

func NewOwner(directory string) *Owner {
	return &Owner{directory: directory}
}

func (o *Owner) Write(report Report) error {
	if o.lock == nil {
		if err := os.MkdirAll(o.directory, 0o700); err != nil {
			return fmt.Errorf("status: %w", err)
		}
		lock, err := takeLock(filepath.Join(o.directory, lockFileName))
		if err != nil {
			return err
		}
		o.lock = lock
	}
	return write(filepath.Join(o.directory, FileName), report)
}

func (o *Owner) Owns() bool {
	return o.lock != nil
}

func Running(directory string) bool {
	return lockIsTaken(filepath.Join(directory, lockFileName))
}

func write(path string, report Report) error {
	var text bytes.Buffer
	fmt.Fprintf(&text, "state=%s\n", oneLine(string(report.State)))
	for _, machine := range report.Machines {
		fmt.Fprintf(&text, "machine=%s\n", oneLine(machine))
	}
	if report.Address != "" {
		fmt.Fprintf(&text, "address=%s\n", oneLine(report.Address))
	}
	if report.Error != "" {
		fmt.Fprintf(&text, "error=%s\n", oneLine(report.Error))
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("status: %w", err)
	}
	defer os.Remove(temporary.Name())
	if _, err := temporary.Write(text.Bytes()); err != nil {
		temporary.Close()
		return fmt.Errorf("status: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("status: %w", err)
	}
	if err := os.Rename(temporary.Name(), path); err != nil {
		return fmt.Errorf("status: %w", err)
	}
	return nil
}

func Read(directory string) (Report, error) {
	contents, err := os.ReadFile(filepath.Join(directory, FileName))
	if err != nil {
		return Report{}, err
	}
	var report Report
	lines := bufio.NewScanner(bytes.NewReader(contents))
	for lines.Scan() {
		key, value, found := strings.Cut(lines.Text(), "=")
		if !found {
			continue
		}
		switch key {
		case "state":
			report.State = State(value)
		case "machine":
			report.Machines = append(report.Machines, value)
		case "address":
			report.Address = value
		case "error":
			report.Error = value
		}
	}
	return report, lines.Err()
}

func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
