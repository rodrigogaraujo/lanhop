//go:build !(linux && (386 || amd64 || arm || arm64 || loong64 || riscv64))

package main

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
)

func TestReceiveSaysWhenThisSystemCannotReplayInput(t *testing.T) {
	trustAlready(t, useConfigDirectory(t), trustedDesk)
	var stdout, stderr bytes.Buffer

	if code := run([]string{"receive"}, nobodyTyping(), &stdout, &stderr); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}

	if !strings.Contains(stderr.String(), "not supported on "+runtime.GOOS) {
		t.Errorf("stderr = %q, want it to say that receiving is not supported on %s", stderr.String(), runtime.GOOS)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want nothing", stdout.String())
	}
}
