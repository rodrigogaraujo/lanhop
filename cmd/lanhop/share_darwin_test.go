//go:build darwin && cgo

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestShareWithoutThePermissionSaysHowToGrantIt(t *testing.T) {
	capture, err := captureWhenAllowed(false)
	if capture != nil {
		t.Error("a capture was started without the permission")
	}
	if err == nil || !strings.Contains(err.Error(), "Accessibility") || !strings.Contains(err.Error(), "docs/setup-macos.md") {
		t.Errorf("error = %v, want it to name the Accessibility permission and the setup guide", err)
	}
}

func TestDevicesReportsWhatThisMacAllows(t *testing.T) {
	cases := []struct {
		name                string
		canRead, canControl bool
		wantLines           []string
		wantsHelp           bool
	}{
		{"everything allowed", true, true, []string{"read the keyboard and mouse:    yes", "press keys and move the mouse:  yes"}, false},
		{"nothing allowed", false, false, []string{"read the keyboard and mouse:    no", "press keys and move the mouse:  no"}, true},
		{"reading only", true, false, []string{"read the keyboard and mouse:    yes", "press keys and move the mouse:  no"}, true},
		{"controlling only", false, true, []string{"read the keyboard and mouse:    no", "press keys and move the mouse:  yes"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer

			describePermissions(&out, c.canRead, c.canControl)

			for _, line := range c.wantLines {
				if !strings.Contains(out.String(), line) {
					t.Errorf("output %q does not contain %q", out.String(), line)
				}
			}
			if helped := strings.Contains(out.String(), "docs/setup-macos.md"); helped != c.wantsHelp {
				t.Errorf("mentions the setup guide = %v, want %v", helped, c.wantsHelp)
			}
		})
	}
}
