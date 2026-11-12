//go:build !unix

package main

import "os/exec"

func inAProcessGroupOfItsOwn(*exec.Cmd) {}
