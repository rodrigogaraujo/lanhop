//go:build !(linux && (386 || amd64 || arm || arm64 || loong64 || riscv64))

package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestShareSaysThatThisSystemCannotShareYetAndTouchesNothing(t *testing.T) {
	directory := useConfigDirectory(t)
	var stdout, stderr bytes.Buffer

	code := run([]string{"share"}, nobodyTyping(), &stdout, &stderr)

	if code != 1 || !strings.Contains(stderr.String(), "is not supported on") {
		t.Errorf("exit code = %d, stderr = %q, want 1 and a message that says sharing is not supported here", code, stderr.String())
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Errorf("the config directory was created: %v", err)
	}
}
