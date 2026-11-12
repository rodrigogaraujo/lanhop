package layoutedit

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rodrigogaraujo/lanhop/internal/layout"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

const (
	directoryMode = 0o700

	thisMachine  = " (this machine)"
	notAName     = "%q is not the name of a machine."
	ownNotPlaced = "This machine, %q, is not on the desk. Put it in a place, so that the edges of its screen lead somewhere."
)

type Trusted interface {
	Peers() []trust.Peer
}

type Editor struct {
	Path    string
	Own     string
	Trusted Trusted
}

func (e Editor) machines() []string {
	names := []string{e.Own}
	for _, peer := range e.Trusted.Peers() {
		if peer.Name != e.Own {
			names = append(names, peer.Name)
		}
	}
	return names
}

func (e Editor) Read() (*layout.Layout, error) {
	file, err := os.Open(e.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return &layout.Layout{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	machines := e.machines()
	return layout.Parse(file, func(name string) bool { return slices.Contains(machines, name) })
}

func (e Editor) understood(rows [][]string) (text string, desk *layout.Layout, problem string) {
	placed := trimmed(rows)
	ownIsPlaced := false
	for _, row := range placed {
		for _, cell := range row {
			if cell != "" && !trust.ValidName(cell) {
				return "", nil, fmt.Sprintf(notAName, cell)
			}
			ownIsPlaced = ownIsPlaced || cell == e.Own
		}
	}
	if !ownIsPlaced {
		return "", nil, fmt.Sprintf(ownNotPlaced, e.Own)
	}
	text = layout.Draw(placed)
	machines := e.machines()
	desk, err := layout.Parse(strings.NewReader(text), func(name string) bool { return slices.Contains(machines, name) })
	if err != nil {
		return "", nil, err.Error()
	}
	return text, desk, ""
}

func (e Editor) look(rows [][]string) view {
	seen := view{Rows: framed(rows), Neighbours: []string{}}
	_, desk, problem := e.understood(rows)
	if problem != "" {
		seen.Problem = problem
		return seen
	}
	for _, machine := range desk.Machines() {
		name := machine
		if machine == e.Own {
			name += thisMachine
		}
		seen.Neighbours = append(seen.Neighbours, name+": "+desk.NeighboursOf(machine))
	}
	return seen
}

func (e Editor) save(rows [][]string) (problem string, err error) {
	text, _, problem := e.understood(rows)
	if problem != "" {
		return problem, nil
	}
	return "", replaceFile(e.Path, text)
}

func replaceFile(path, contents string) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, directoryMode); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if _, err := temporary.WriteString(contents); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), path)
}
