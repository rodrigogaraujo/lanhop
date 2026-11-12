//go:build unix

package main

import (
	"os/exec"
	"syscall"
)

func inAProcessGroupOfItsOwn(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
