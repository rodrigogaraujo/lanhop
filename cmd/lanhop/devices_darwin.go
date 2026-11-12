//go:build darwin && cgo

package main

import (
	"fmt"
	"io"

	"github.com/rodrigogaraujo/lanhop/internal/macos"
)

const permissions = `On a Mac, lanhop reads and controls the keyboard and mouse through macOS, so there are no devices to list.

  allowed to read the keyboard and mouse:    %s
  allowed to press keys and move the mouse:  %s
`

const howToAllow = `
To allow what says no, open System Settings > Privacy & Security > Accessibility and allow the program that starts lanhop.
docs/setup-macos.md explains how.
`

func listDevices(out io.Writer) error {
	describePermissions(out, macos.CanListen(), macos.CanPost())
	return nil
}

func describePermissions(out io.Writer, canRead, canControl bool) {
	fmt.Fprintf(out, permissions, yesOrNo(canRead), yesOrNo(canControl))
	if !canRead || !canControl {
		fmt.Fprint(out, howToAllow)
	}
}

func yesOrNo(allowed bool) string {
	if allowed {
		return "yes"
	}
	return "no"
}
