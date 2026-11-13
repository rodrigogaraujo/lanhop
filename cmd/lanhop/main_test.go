package main

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"github.com/rodrigogaraujo/lanhop/internal/pairing"
)

func nobodyTyping() *strings.Reader {
	return strings.NewReader("")
}

func TestVersionCommandPrintsTheVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"version"}, nobodyTyping(), &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if got, want := stdout.String(), "lanhop "+version+"\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestUnknownCommandFailsAndShowsUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"teleport"}, nobodyTyping(), &stdout, &stderr); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), `unknown command "teleport"`) || !strings.Contains(stderr.String(), "Usage:") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestNoCommandFailsAndShowsUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, nobodyTyping(), &stdout, &stderr); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestHelpNamesEveryCommandAndThePairingPort(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"help"}, nobodyTyping(), &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	mentions := []string{
		"lanhop pair ", "lanhop pair ADDRESS", "lanhop peers", "lanhop forget NAME", "lanhop devices", "lanhop version",
		"--as NAME", "--listen ADDRESS", "port " + strconv.Itoa(pairing.DefaultPort), configDirectoryVariable,
	}
	for _, mention := range mentions {
		if !strings.Contains(stdout.String(), mention) {
			t.Errorf("help does not mention %q", mention)
		}
	}
}

func TestUsageNamesTheStatusCommandThatTheInstallerLooksFor(t *testing.T) {
	if !strings.Contains(usage, "lanhop status") {
		t.Error(`the usage does not name "lanhop status", so install.sh would take this lanhop for one without it`)
	}
}
