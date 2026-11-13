package status

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	FileName       = "status"
	lockFileName   = "status.lock"
	lockAttempts   = 5
	lockRetryPause = 10 * time.Millisecond
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

var errTakenByAnother = errors.New("status: another lanhop on this machine keeps the status")

type Owner struct {
	directory string
	mutex     sync.Mutex
	lock      *os.File
}

var (
	ownersMutex sync.Mutex
	owners      = map[string]*Owner{}
)

func OwnerOf(directory string) *Owner {
	ownersMutex.Lock()
	defer ownersMutex.Unlock()
	owner, found := owners[directory]
	if !found {
		owner = &Owner{directory: directory}
		owners[directory] = owner
	}
	return owner
}

func (o *Owner) Write(report Report) error {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	if o.lock == nil {
		if err := os.MkdirAll(o.directory, 0o700); err != nil {
			return fmt.Errorf("status: %w", err)
		}
		lock, err := takeLockPatiently(filepath.Join(o.directory, lockFileName))
		if err != nil {
			return err
		}
		o.lock = lock
	}
	return write(filepath.Join(o.directory, FileName), report)
}

func (o *Owner) Owns() bool {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	return o.lock != nil
}

func takeLockPatiently(path string) (*os.File, error) {
	for attempt := 1; ; attempt++ {
		lock, err := takeLock(path)
		if !errors.Is(err, errTakenByAnother) || attempt == lockAttempts {
			return lock, err
		}
		time.Sleep(lockRetryPause)
	}
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
