//go:build !unix

package main

func waitForTheUpdateLock(string) (func(), error) {
	return func() {}, nil
}
