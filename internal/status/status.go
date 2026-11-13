package status

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const FileName = "status"

type State string

const (
	Accessibility State = "accessibility"
	Connecting    State = "connecting"
	Connected     State = "connected"
	NoRoute       State = "no-route"
	Failing       State = "failing"
	Waiting       State = "waiting"
	Sharing       State = "sharing"
	Stopped       State = "stopped"
)

type Report struct {
	PID      int
	State    State
	Machines []string
	Address  string
	Error    string
}

func Write(path string, report Report) error {
	var text bytes.Buffer
	fmt.Fprintf(&text, "pid=%d\n", report.PID)
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

func Read(path string) (Report, error) {
	contents, err := os.ReadFile(path)
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
		case "pid":
			report.PID, _ = strconv.Atoi(value)
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

func Running(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}

func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
