//go:build linux && containertest && (386 || amd64 || arm || arm64 || loong64 || riscv64)

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/event"
)

type testInputLog struct {
	mutex   sync.Mutex
	written strings.Builder
}

func (l *testInputLog) Write(p []byte) (int, error) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	return l.written.Write(p)
}

func (l *testInputLog) String() string {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	return l.written.String()
}

func writeTestInput(t *testing.T, path, lines string) {
	t.Helper()
	writer, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open the FIFO to write: %v", err)
	}
	defer writer.Close()
	if _, err := fmt.Fprint(writer, lines); err != nil {
		t.Fatalf("write to the FIFO: %v", err)
	}
}

func expectCaptured(t *testing.T, capture *lineCapture, want ...event.Event) {
	t.Helper()
	for _, expected := range want {
		select {
		case got := <-capture.Events():
			if got != expected {
				t.Fatalf("captured %+v, want %+v", got, expected)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("nothing captured, want %+v", expected)
		}
	}
}

func TestTheContainerTestInputTurnsEachLineIntoAnEventAndOutlivesItsWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input")
	var log testInputLog
	capture, err := startLineCapture(path, &log)
	if err != nil {
		t.Fatalf("startLineCapture: %v", err)
	}
	defer capture.Close()

	writeTestInput(t, path, "1 29 1\n0 0 0\n\nnot an event\n1 2\n2 0 -5\n3 1 65535\n")
	expectCaptured(t, capture, event.Key(29, event.KeyPressed), event.Sync(), event.Relative(event.RelX, -5), event.Absolute(event.AbsY, 65535))
	writeTestInput(t, path, "1 272 0\n")
	expectCaptured(t, capture, event.Key(event.ButtonLeft, event.KeyReleased))

	for _, refused := range []string{`"not an event"`, `"1 2"`} {
		if !strings.Contains(log.String(), refused) {
			t.Errorf("the log does not name the line %s it refused:\n%s", refused, log.String())
		}
	}
	if err := capture.Take(); err != nil {
		t.Errorf("Take: %v", err)
	}
	if err := capture.GiveBack(); err != nil {
		t.Errorf("GiveBack: %v", err)
	}
	if err := capture.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	select {
	case _, open := <-capture.Events():
		if open {
			t.Error("an event came after Close")
		}
	case <-time.After(3 * time.Second):
		t.Error("Events was not closed after Close")
	}
}

func TestTheContainerTestInputRefusesAPathThatIsNotAFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plain")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if capture, err := startLineCapture(path, &testInputLog{}); err == nil {
		capture.Close()
		t.Fatal("a plain file was taken as the FIFO of the test input")
	}
}

func TestTheContainerTestBuildSaysWhichVariableNamesItsInput(t *testing.T) {
	t.Setenv(testInputVariable, "")
	if _, err := startCapture(); err == nil || !strings.Contains(err.Error(), testInputVariable) {
		t.Errorf("startCapture = %v, want an error that names %s", err, testInputVariable)
	}
}
