package layoutedit

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/rodrigogaraujo/lanhop/internal/layout"
)

func sameRows(got, want [][]string) bool {
	return slices.EqualFunc(got, want, slices.Equal[[]string])
}

func rowOf(machines int) []string {
	row := make([]string, machines)
	for i := range row {
		row[i] = fmt.Sprintf("m%d", i)
	}
	return row
}

func columnOf(machines int) [][]string {
	column := make([][]string, machines)
	for i := range column {
		column[i] = []string{fmt.Sprintf("m%d", i)}
	}
	return column
}

func emptyRows(rows, columns int) [][]string {
	grid := make([][]string, rows)
	for i := range grid {
		grid[i] = make([]string, columns)
	}
	return grid
}

func around(desk [][]string, above, beside int) [][]string {
	grid := emptyRows(len(desk)+2*above, len(desk[0])+2*beside)
	for row, cells := range desk {
		copy(grid[above+row][beside:], cells)
	}
	return grid
}

func TestTrimmedKeepsOnlyTheRowsAndColumnsBetweenTheOutermostMachines(t *testing.T) {
	cases := []struct {
		name string
		rows [][]string
		want [][]string
	}{
		{
			name: "a desk with nothing around it stays as it is",
			rows: [][]string{{"a", "b"}, {"", "c"}},
			want: [][]string{{"a", "b"}, {"", "c"}},
		},
		{
			name: "empty rows and columns around the machines go away",
			rows: [][]string{{"", "", "", ""}, {"", "a", "b", ""}, {"", "", "c", ""}, {"", "", "", ""}},
			want: [][]string{{"a", "b"}, {"", "c"}},
		},
		{
			name: "empty rows and columns between machines stay",
			rows: [][]string{{"", "", "", ""}, {"a", "", "b", ""}, {"", "", "", ""}, {"c", "", "", ""}},
			want: [][]string{{"a", "", "b"}, {"", "", ""}, {"c", "", ""}},
		},
		{
			name: "one machine far from the corner",
			rows: [][]string{{"", "", ""}, {"", "", ""}, {"", "", "a"}},
			want: [][]string{{"a"}},
		},
		{
			name: "machines on a diagonal",
			rows: [][]string{{"", "", "a"}, {"", "b", ""}, {"c", "", ""}, {"", "", ""}},
			want: [][]string{{"", "", "a"}, {"", "b", ""}, {"c", "", ""}},
		},
		{
			name: "rows of different lengths become a rectangle",
			rows: [][]string{{"", "a", "b", "c"}, {"", "d"}, {}, {"", "", "e"}},
			want: [][]string{{"a", "b", "c"}, {"d", "", ""}, {"", "", ""}, {"", "e", ""}},
		},
		{
			name: "a row shorter than the first column in use",
			rows: [][]string{{"", "", "a"}, {""}, {"", "", "b"}},
			want: [][]string{{"a"}, {""}, {"b"}},
		},
		{
			name: "no machine at all",
			rows: [][]string{{"", ""}, {"", ""}},
			want: nil,
		},
		{
			name: "no row at all",
			rows: nil,
			want: nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := trimmed(c.rows); !sameRows(got, c.want) {
				t.Errorf("trimmed(%q) = %q, want %q", c.rows, got, c.want)
			}
		})
	}
}

func TestFramedLeavesOneEmptyRowAndOneEmptyColumnAroundTheMachines(t *testing.T) {
	cases := []struct {
		name string
		rows [][]string
		want [][]string
	}{
		{
			name: "an empty desk is three by three",
			rows: nil,
			want: emptyRows(3, 3),
		},
		{
			name: "one machine sits in the middle of three by three",
			rows: [][]string{{"a"}},
			want: [][]string{{"", "", ""}, {"", "a", ""}, {"", "", ""}},
		},
		{
			name: "a machine put on the edge of the grid gets a new empty row and column beyond it",
			rows: [][]string{{"", "", "b"}, {"", "a", ""}, {"", "", ""}},
			want: [][]string{{"", "", "", ""}, {"", "", "b", ""}, {"", "a", "", ""}, {"", "", "", ""}},
		},
		{
			name: "rows and columns left empty at the edge shrink back to one",
			rows: [][]string{{"", "", "", "", ""}, {"", "", "", "", ""}, {"", "", "a", "b", ""}, {"", "", "", "", ""}, {"", "", "", "", ""}},
			want: [][]string{{"", "", "", ""}, {"", "a", "b", ""}, {"", "", "", ""}},
		},
		{
			name: "a desk read from a file with rows of different lengths",
			rows: [][]string{{"a", "b", "c"}, {"d"}},
			want: [][]string{{"", "", "", "", ""}, {"", "a", "b", "c", ""}, {"", "d", "", "", ""}, {"", "", "", "", ""}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := framed(c.rows); !sameRows(got, c.want) {
				t.Errorf("framed(%q) = %q, want %q", c.rows, got, c.want)
			}
		})
	}
}

func TestFramedStopsGrowingASideThatHasNinePlaces(t *testing.T) {
	cases := []struct {
		name string
		rows [][]string
		want [][]string
	}{
		{
			name: "a row of eight machines can still grow to either side",
			rows: [][]string{rowOf(8)},
			want: around([][]string{rowOf(8)}, 1, 1),
		},
		{
			name: "a row of nine machines grows only up and down",
			rows: [][]string{rowOf(9)},
			want: around([][]string{rowOf(9)}, 1, 0),
		},
		{
			name: "a column of nine machines grows only to the sides",
			rows: columnOf(9),
			want: around(columnOf(9), 0, 1),
		},
		{
			name: "two machines nine places apart count as nine places",
			rows: [][]string{{"a", "", "", "", "", "", "", "", "b"}},
			want: around([][]string{{"a", "", "", "", "", "", "", "", "b"}}, 1, 0),
		},
		{
			name: "a row longer than nine, written by hand, is shown whole",
			rows: [][]string{rowOf(11)},
			want: around([][]string{rowOf(11)}, 1, 0),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := framed(c.rows); !sameRows(got, c.want) {
				t.Errorf("framed(%q) = %q, want %q", c.rows, got, c.want)
			}
		})
	}
}

func TestFramingADeskTwiceChangesNothing(t *testing.T) {
	for _, rows := range [][][]string{nil, {{"a"}}, {{"a", "", "b"}, {"", "c", ""}}, {rowOf(9)}, columnOf(9)} {
		once := framed(rows)
		if twice := framed(once); !sameRows(twice, once) {
			t.Errorf("framed(%q) = %q, and framing that again gave %q", rows, once, twice)
		}
	}
}

func everyDeskOfThreeMachinesOnThreeByThree() [][][]string {
	var desks [][][]string
	for a := 0; a < 9; a++ {
		for b := 0; b < 9; b++ {
			for c := 0; c < 9; c++ {
				if a == b || a == c || b == c {
					continue
				}
				desk := emptyRows(3, 3)
				desk[a/3][a%3] = "laptop"
				desk[b/3][b%3] = "desk-top"
				desk[c/3][c%3] = "mac_3"
				desks = append(desks, desk)
			}
		}
	}
	return desks
}

func TestEveryPlacementOfThreeMachinesIsReadBackByTheLayoutParserAsItWasArranged(t *testing.T) {
	desks := everyDeskOfThreeMachinesOnThreeByThree()
	if len(desks) != 9*8*7 {
		t.Fatalf("%d desks were tried, want %d", len(desks), 9*8*7)
	}
	for _, desk := range desks {
		text := layout.Draw(trimmed(framed(desk)))

		read, err := layout.Parse(strings.NewReader(text), func(string) bool { return true })
		if err != nil {
			t.Fatalf("the desk %q was written as\n%s\nwhich the parser refuses: %v", desk, text, err)
		}

		if got := read.Rows(); !sameRows(got, trimmed(desk)) {
			t.Errorf("the desk %q was written as\n%s\nand read back as %q, want %q", desk, text, got, trimmed(desk))
		}
		for row, cells := range desk {
			for column, machine := range cells {
				if machine == "" {
					continue
				}
				for side, next := range map[layout.Side][2]int{
					layout.Left:  {row, column - 1},
					layout.Right: {row, column + 1},
					layout.Above: {row - 1, column},
					layout.Below: {row + 1, column},
				} {
					want := ""
					if next[0] >= 0 && next[0] < 3 && next[1] >= 0 && next[1] < 3 {
						want = desk[next[0]][next[1]]
					}
					if got, _ := read.Neighbour(machine, side); got != want {
						t.Errorf("on the desk %q, written as\n%s\n%s has %q on the %s, want %q", desk, text, machine, got, side, want)
					}
				}
			}
		}
	}
}
