//go:build !(darwin && cgo)

package main

import "os"

func runOffTheMainThread(program func() int) {
	os.Exit(program())
}
