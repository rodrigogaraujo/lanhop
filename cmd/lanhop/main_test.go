package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionCommandPrintsTheVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if got, want := stdout.String(), "lanhop "+version+"\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestUnknownCommandFailsAndShowsUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"teleport"}, &stdout, &stderr); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), `unknown command "teleport"`) || !strings.Contains(stderr.String(), "Usage:") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestNoCommandFailsAndShowsUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Errorf("stderr = %q", stderr.String())
	}
}
