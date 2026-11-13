//go:build linux

package x11

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
)

const socketDirectory = "/tmp/.X11-unix"

var (
	ErrNoDisplay     = errors.New("x11: no X server to talk to")
	ErrRemoteDisplay = errors.New("x11: only an X server on this machine is supported")
	errNoCookie      = errors.New("x11: no cookie for this display")
	cookieName       = "MIT-MAGIC-COOKIE-1"
	familyLocal      = uint16(256)
	familyWild       = uint16(65535)
)

type display struct {
	number string
	socket string
}

func parseDisplay(value string) (display, error) {
	host, rest, found := strings.Cut(value, ":")
	if !found {
		return display{}, fmt.Errorf("x11: DISPLAY %q has no display number", value)
	}
	if host != "" && host != "unix" {
		return display{}, fmt.Errorf("%w: DISPLAY is %q", ErrRemoteDisplay, value)
	}
	number, _, _ := strings.Cut(rest, ".")
	if _, err := strconv.Atoi(number); err != nil {
		return display{}, fmt.Errorf("x11: DISPLAY %q has no display number", value)
	}
	return display{number: number, socket: filepath.Join(socketDirectory, "X"+number)}, nil
}

func displaysOnThisMachine(directory string, owners ...uint32) []display {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil
	}
	var found []display
	for _, entry := range entries {
		number, isDisplay := strings.CutPrefix(entry.Name(), "X")
		if !isDisplay {
			continue
		}
		if _, err := strconv.Atoi(number); err != nil {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		if !ownedByOneOf(path, owners) {
			continue
		}
		found = append(found, display{number: number, socket: path})
	}
	slices.SortFunc(found, func(a, b display) int {
		x, _ := strconv.Atoi(a.number)
		y, _ := strconv.Atoi(b.number)
		return x - y
	})
	return found
}

func ownedByOneOf(path string, owners []uint32) bool {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && slices.Contains(owners, stat.Uid)
}

func readField(r io.Reader) ([]byte, error) {
	var length uint16
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return nil, err
	}
	field := make([]byte, length)
	_, err := io.ReadFull(r, field)
	return field, err
}

func cookieFor(number, host string, authority []byte) ([]byte, error) {
	r := bytes.NewReader(authority)
	for {
		var family uint16
		if err := binary.Read(r, binary.BigEndian, &family); err != nil {
			return nil, errNoCookie
		}
		var fields [4][]byte
		for i := range fields {
			field, err := readField(r)
			if err != nil {
				return nil, errNoCookie
			}
			fields[i] = field
		}
		address, entryNumber, name, secret := string(fields[0]), string(fields[1]), string(fields[2]), fields[3]
		forThisMachine := family == familyWild || (family == familyLocal && address == host)
		if forThisMachine && entryNumber == number && name == cookieName {
			return secret, nil
		}
	}
}

func sessionEnvironment(output []byte) (displayValue, authority string) {
	for _, line := range strings.Split(string(output), "\n") {
		if value, found := strings.CutPrefix(line, "DISPLAY="); found {
			displayValue = value
		}
		if value, found := strings.CutPrefix(line, "XAUTHORITY="); found {
			authority = value
		}
	}
	return displayValue, authority
}

func authorityFile() string {
	if path := os.Getenv("XAUTHORITY"); path != "" {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".Xauthority")
}

func cookieFromFile(number, path string) []byte {
	if path == "" {
		return nil
	}
	authority, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	host, _ := os.Hostname()
	secret, err := cookieFor(number, host, authority)
	if err != nil {
		return nil
	}
	return secret
}
