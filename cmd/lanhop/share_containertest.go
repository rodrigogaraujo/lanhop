//go:build linux && containertest && (386 || amd64 || arm || arm64 || loong64 || riscv64)

package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/rodrigogaraujo/lanhop/internal/event"
)

const testInputVariable = "LANHOP_TEST_INPUT"

func startCapture() (inputCapture, error) {
	path := os.Getenv(testInputVariable)
	if path == "" {
		return nil, fmt.Errorf("this lanhop is built for the container test and reads its input from a FIFO: set %s to its path", testInputVariable)
	}
	capture, err := startLineCapture(path, os.Stderr)
	if err != nil {
		return nil, err
	}
	return capture, nil
}

type lineCapture struct {
	fifo    *os.File
	log     io.Writer
	events  chan event.Event
	stopped chan struct{}
	closing sync.Once
}

func startLineCapture(path string, log io.Writer) (*lineCapture, error) {
	if err := syscall.Mkfifo(path, 0o600); err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, fmt.Errorf("cannot make the FIFO of the test input: %w", err)
	}
	fifo, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("cannot open the FIFO of the test input: %w", err)
	}
	info, err := fifo.Stat()
	if err == nil && info.Mode()&fs.ModeNamedPipe == 0 {
		err = fmt.Errorf("%s is not a FIFO", path)
	}
	if err != nil {
		fifo.Close()
		return nil, err
	}
	capture := &lineCapture{fifo: fifo, log: log, events: make(chan event.Event), stopped: make(chan struct{})}
	go capture.read()
	return capture, nil
}

func (c *lineCapture) read() {
	defer close(c.events)
	lines := bufio.NewScanner(c.fifo)
	for lines.Scan() {
		if strings.TrimSpace(lines.Text()) == "" {
			continue
		}
		captured, err := parseInputLine(lines.Text())
		if err != nil {
			fmt.Fprintf(c.log, "test input: %v\n", err)
			continue
		}
		select {
		case c.events <- captured:
		case <-c.stopped:
			return
		}
	}
}

func parseInputLine(line string) (event.Event, error) {
	fields := strings.Fields(line)
	if len(fields) == 3 {
		kind, errKind := strconv.ParseUint(fields[0], 10, 16)
		code, errCode := strconv.ParseUint(fields[1], 10, 16)
		value, errValue := strconv.ParseInt(fields[2], 10, 32)
		if errKind == nil && errCode == nil && errValue == nil {
			return event.Event{Type: uint16(kind), Code: uint16(code), Value: int32(value)}, nil
		}
	}
	return event.Event{}, fmt.Errorf("%q is not three whole numbers: type code value", line)
}

func (c *lineCapture) Events() <-chan event.Event {
	return c.events
}

func (c *lineCapture) Take() error {
	fmt.Fprintln(c.log, "test input: focus left this machine")
	return nil
}

func (c *lineCapture) GiveBack() error {
	fmt.Fprintln(c.log, "test input: focus is on this machine")
	return nil
}

func (c *lineCapture) Close() error {
	var err error
	c.closing.Do(func() {
		close(c.stopped)
		err = c.fifo.Close()
	})
	return err
}
