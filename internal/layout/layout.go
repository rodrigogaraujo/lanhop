package layout

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

const (
	hole        = "."
	commentSign = "#"
	separators  = " \t"
	columnGap   = 2
	wholeFile   = 0

	invalidName  = "%q is not a valid name: use 1 to %d letters, digits, hyphens or underscores"
	repeatedName = "%q is repeated, first seen on line %d: give each machine one place"
	unknownName  = "%q is not a known machine: use the name of this machine or one that \"lanhop peers\" lists"
	noMachine    = "the layout has no machine: write the names of the machines in a row, from left to right"
)

type Side int

const (
	Left Side = iota
	Right
	Above
	Below
)

func (s Side) String() string {
	switch s {
	case Left:
		return "left"
	case Right:
		return "right"
	case Above:
		return "above"
	case Below:
		return "below"
	}
	return fmt.Sprintf("Side(%d)", int(s))
}

type Problem struct {
	Line  int
	Cause string
}

func (p Problem) String() string {
	if p.Line == wholeFile {
		return p.Cause
	}
	return fmt.Sprintf("line %d: %s", p.Line, p.Cause)
}

type Problems []Problem

func (p Problems) Error() string {
	lines := make([]string, len(p))
	for i, problem := range p {
		lines[i] = problem.String()
	}
	return strings.Join(lines, "\n")
}

type place struct{ row, column, line int }

type Layout struct {
	rows      [][]string
	places    map[string]place
	commented bool
}

func Parse(source io.Reader, known func(name string) bool) (*Layout, error) {
	contents, err := io.ReadAll(source)
	if err != nil {
		return nil, err
	}
	parsed := &Layout{places: map[string]place{}}
	var problems Problems
	named := false
	for index, text := range strings.Split(string(contents), "\n") {
		line := index + 1
		cells, commented := cellsOf(text)
		parsed.commented = parsed.commented || commented
		if len(cells) == 0 {
			continue
		}
		row := make([]string, len(cells))
		for column, cell := range cells {
			if cell == hole {
				continue
			}
			named = true
			first, repeated := parsed.places[cell]
			switch {
			case !trust.ValidName(cell):
				problems = append(problems, Problem{Line: line, Cause: fmt.Sprintf(invalidName, cell, trust.MaxNameLength)})
			case repeated:
				problems = append(problems, Problem{Line: line, Cause: fmt.Sprintf(repeatedName, cell, first.line)})
			default:
				if !known(cell) {
					problems = append(problems, Problem{Line: line, Cause: fmt.Sprintf(unknownName, cell)})
				}
				row[column] = cell
				parsed.places[cell] = place{row: len(parsed.rows), column: column, line: line}
			}
		}
		parsed.rows = append(parsed.rows, row)
	}
	if !named {
		problems = append(problems, Problem{Line: wholeFile, Cause: noMachine})
	}
	if len(problems) > 0 {
		return nil, problems
	}
	return parsed, nil
}

func cellsOf(text string) (cells []string, commented bool) {
	drawn, _, commented := strings.Cut(strings.TrimSuffix(text, "\r"), commentSign)
	return strings.FieldsFunc(drawn, func(character rune) bool {
		return strings.ContainsRune(separators, character)
	}), commented
}

func (l *Layout) Neighbour(name string, side Side) (string, bool) {
	at, placed := l.places[name]
	if !placed {
		return "", false
	}
	switch side {
	case Left:
		at.column--
	case Right:
		at.column++
	case Above:
		at.row--
	case Below:
		at.row++
	default:
		return "", false
	}
	neighbour := l.machineAt(at.row, at.column)
	return neighbour, neighbour != ""
}

func (l *Layout) machineAt(row, column int) string {
	if row < 0 || row >= len(l.rows) {
		return ""
	}
	if column < 0 || column >= len(l.rows[row]) {
		return ""
	}
	return l.rows[row][column]
}

func (l *Layout) Machines() []string {
	var machines []string
	for _, row := range l.rows {
		for _, machine := range row {
			if machine != "" {
				machines = append(machines, machine)
			}
		}
	}
	return machines
}

func (l *Layout) Has(name string) bool {
	_, placed := l.places[name]
	return placed
}

func (l *Layout) HasComments() bool {
	return l.commented
}

func (l *Layout) Rows() [][]string {
	rows := make([][]string, len(l.rows))
	for i, row := range l.rows {
		rows[i] = slices.Clone(row)
	}
	return rows
}

func Draw(rows [][]string) string {
	var text strings.Builder
	widths := columnWidths(rows)
	for _, row := range rows {
		for column, machine := range row {
			cell := machine
			if cell == "" {
				cell = hole
			}
			if column == len(row)-1 {
				text.WriteString(cell + "\n")
				continue
			}
			fmt.Fprintf(&text, "%-*s", widths[column]+columnGap, cell)
		}
	}
	return text.String()
}

func (l *Layout) Describe() string {
	var text strings.Builder
	text.WriteString(Draw(l.rows) + "\n")
	for _, machine := range l.Machines() {
		fmt.Fprintf(&text, "%s: %s\n", machine, l.NeighboursOf(machine))
	}
	return text.String()
}

func columnWidths(rows [][]string) []int {
	var widths []int
	for _, row := range rows {
		for column, machine := range row {
			if column == len(widths) {
				widths = append(widths, len(hole))
			}
			widths[column] = max(widths[column], len(machine))
		}
	}
	return widths
}

func (l *Layout) NeighboursOf(machine string) string {
	var around []string
	for _, side := range []Side{Left, Right, Above, Below} {
		if neighbour, found := l.Neighbour(machine, side); found {
			around = append(around, side.String()+" "+neighbour)
		}
	}
	if len(around) == 0 {
		return "no neighbours"
	}
	return strings.Join(around, ", ")
}
