package layout

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

const (
	longestName = "abcdefghijklmnopqrstuvwxyz012345"
	planExample = "# my desk\nlaptop   desktop   macbook\n.        server    .\n"
)

type neighbours struct{ left, right, above, below string }

func everyNameIsKnown(string) bool { return true }

func onlyKnown(names ...string) func(string) bool {
	return func(name string) bool { return slices.Contains(names, name) }
}

func parse(t *testing.T, text string) *Layout {
	t.Helper()
	parsed, err := Parse(strings.NewReader(text), everyNameIsKnown)
	if err != nil {
		t.Fatalf("Parse(%q): %v", text, err)
	}
	return parsed
}

func problemsOf(t *testing.T, text string, known func(string) bool) Problems {
	t.Helper()
	parsed, err := Parse(strings.NewReader(text), known)
	var problems Problems
	if !errors.As(err, &problems) {
		t.Fatalf("Parse(%q) error = %v, want the problems of the file", text, err)
	}
	if parsed != nil {
		t.Errorf("Parse(%q) returned a layout together with problems", text)
	}
	return problems
}

func neighbourOn(t *testing.T, parsed *Layout, name string, side Side) string {
	t.Helper()
	neighbour, found := parsed.Neighbour(name, side)
	if found != (neighbour != "") {
		t.Errorf("Neighbour(%q, %v) = %q, %v: the name and the flag disagree", name, side, neighbour, found)
	}
	return neighbour
}

func neighboursOf(t *testing.T, parsed *Layout, name string) neighbours {
	t.Helper()
	return neighbours{
		left:  neighbourOn(t, parsed, name, Left),
		right: neighbourOn(t, parsed, name, Right),
		above: neighbourOn(t, parsed, name, Above),
		below: neighbourOn(t, parsed, name, Below),
	}
}

func TestParseReadsTheDeskAsAGrid(t *testing.T) {
	cases := []struct {
		name     string
		text     string
		machines []string
		want     map[string]neighbours
	}{
		{
			name:     "one machine alone has no neighbour",
			text:     "laptop\n",
			machines: []string{"laptop"},
			want:     map[string]neighbours{"laptop": {}},
		},
		{
			name:     "a row places machines from left to right",
			text:     "laptop desktop macbook\n",
			machines: []string{"laptop", "desktop", "macbook"},
			want: map[string]neighbours{
				"laptop":  {right: "desktop"},
				"desktop": {left: "laptop", right: "macbook"},
				"macbook": {left: "desktop"},
			},
		},
		{
			name:     "a row of five machines",
			text:     "macbook-a macbook-b macbook-c server imac\n",
			machines: []string{"macbook-a", "macbook-b", "macbook-c", "server", "imac"},
			want: map[string]neighbours{
				"macbook-a": {right: "macbook-b"},
				"macbook-b": {left: "macbook-a", right: "macbook-c"},
				"macbook-c": {left: "macbook-b", right: "server"},
				"server":    {left: "macbook-c", right: "imac"},
				"imac":      {left: "server"},
			},
		},
		{
			name:     "the next row sits below, place by place",
			text:     "a b\nc d\ne f\n",
			machines: []string{"a", "b", "c", "d", "e", "f"},
			want: map[string]neighbours{
				"a": {right: "b", below: "c"},
				"b": {left: "a", below: "d"},
				"c": {right: "d", above: "a", below: "e"},
				"d": {left: "c", above: "b", below: "f"},
				"e": {right: "f", above: "c"},
				"f": {left: "e", above: "d"},
			},
		},
		{
			name:     "the example of the plan",
			text:     planExample,
			machines: []string{"laptop", "desktop", "macbook", "server"},
			want: map[string]neighbours{
				"laptop":  {right: "desktop"},
				"desktop": {left: "laptop", right: "macbook", below: "server"},
				"macbook": {left: "desktop"},
				"server":  {above: "desktop"},
			},
		},
		{
			name:     "a shorter row below leaves the last places without a neighbour",
			text:     "a b c\nd\n",
			machines: []string{"a", "b", "c", "d"},
			want: map[string]neighbours{
				"a": {right: "b", below: "d"},
				"b": {left: "a", right: "c"},
				"c": {left: "b"},
				"d": {above: "a"},
			},
		},
		{
			name:     "a shorter row above leaves the last places without a neighbour",
			text:     "a\nb c d\n",
			machines: []string{"a", "b", "c", "d"},
			want: map[string]neighbours{
				"a": {below: "b"},
				"b": {right: "c", above: "a"},
				"c": {left: "b", right: "d"},
				"d": {left: "c"},
			},
		},
		{
			name:     "a hole in a row is not jumped over",
			text:     "a . b\n",
			machines: []string{"a", "b"},
			want:     map[string]neighbours{"a": {}, "b": {}},
		},
		{
			name:     "a hole above or below is not jumped over",
			text:     "a b\n. c\nd e\n",
			machines: []string{"a", "b", "c", "d", "e"},
			want: map[string]neighbours{
				"a": {right: "b"},
				"b": {left: "a", below: "c"},
				"c": {above: "b", below: "e"},
				"d": {right: "e"},
				"e": {left: "d", above: "c"},
			},
		},
		{
			name:     "a hole at the start of a row keeps the positions",
			text:     ". a\nb c\n",
			machines: []string{"a", "b", "c"},
			want: map[string]neighbours{
				"a": {below: "c"},
				"b": {right: "c"},
				"c": {left: "b", above: "a"},
			},
		},
		{
			name:     "a row of holes keeps two rows apart",
			text:     "a\n.\nb\n",
			machines: []string{"a", "b"},
			want:     map[string]neighbours{"a": {}, "b": {}},
		},
		{
			name:     "blank lines and comment lines are not rows",
			text:     "# the desk\n\na b\n\n   \n# the shelf\n\t# still the shelf\nc d\n\n",
			machines: []string{"a", "b", "c", "d"},
			want: map[string]neighbours{
				"a": {right: "b", below: "c"},
				"b": {left: "a", below: "d"},
				"c": {right: "d", above: "a"},
				"d": {left: "c", above: "b"},
			},
		},
		{
			name:     "a comment ends a row",
			text:     "a b # c d\ne# f\n",
			machines: []string{"a", "b", "e"},
			want: map[string]neighbours{
				"a": {right: "b", below: "e"},
				"b": {left: "a"},
				"e": {above: "a"},
			},
		},
		{
			name:     "tabs separate machines",
			text:     "a\tb\t\tc\n\t.\td\n",
			machines: []string{"a", "b", "c", "d"},
			want: map[string]neighbours{
				"a": {right: "b"},
				"b": {left: "a", right: "c", below: "d"},
				"c": {left: "b"},
				"d": {above: "b"},
			},
		},
		{
			name:     "spaces around a row do not move the machines",
			text:     "   a b   \n c\n",
			machines: []string{"a", "b", "c"},
			want: map[string]neighbours{
				"a": {right: "b", below: "c"},
				"b": {left: "a"},
				"c": {above: "a"},
			},
		},
		{
			name:     "Windows line endings",
			text:     "# my desk\r\nlaptop desktop\r\n\r\n. server\r\n",
			machines: []string{"laptop", "desktop", "server"},
			want: map[string]neighbours{
				"laptop":  {right: "desktop"},
				"desktop": {left: "laptop", below: "server"},
				"server":  {above: "desktop"},
			},
		},
		{
			name:     "no line ending after the last row",
			text:     "a b\nc",
			machines: []string{"a", "b", "c"},
			want: map[string]neighbours{
				"a": {right: "b", below: "c"},
				"b": {left: "a"},
				"c": {above: "a"},
			},
		},
		{
			name:     "names that differ only in case are different machines",
			text:     "laptop Laptop\n",
			machines: []string{"laptop", "Laptop"},
			want: map[string]neighbours{
				"laptop": {right: "Laptop"},
				"Laptop": {left: "laptop"},
			},
		},
		{
			name:     "every character a name may have",
			text:     "Mac_Book-2 " + longestName + "\n",
			machines: []string{"Mac_Book-2", longestName},
			want: map[string]neighbours{
				"Mac_Book-2": {right: longestName},
				longestName:  {left: "Mac_Book-2"},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			parsed := parse(t, c.text)

			if got := parsed.Machines(); !slices.Equal(got, c.machines) {
				t.Errorf("Machines() = %q, want %q", got, c.machines)
			}
			for name, want := range c.want {
				if !parsed.Has(name) {
					t.Errorf("Has(%q) = false, want true", name)
				}
				if got := neighboursOf(t, parsed, name); got != want {
					t.Errorf("neighbours of %q = %+v, want %+v", name, got, want)
				}
			}
			if len(c.want) != len(c.machines) {
				t.Fatalf("the case checks %d machines and lists %d", len(c.want), len(c.machines))
			}
		})
	}
}

func TestParseReportsEachProblemWithItsLineAndCause(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		known func(string) bool
		want  Problems
	}{
		{
			name:  "a name with a character that is not allowed",
			text:  "laptop desk.top\n",
			known: everyNameIsKnown,
			want: Problems{
				{Line: 1, Cause: `"desk.top" is not a valid name: use 1 to 32 letters, digits, hyphens or underscores`},
			},
		},
		{
			name:  "two dots are not a hole",
			text:  "laptop ..\n",
			known: everyNameIsKnown,
			want: Problems{
				{Line: 1, Cause: `".." is not a valid name: use 1 to 32 letters, digits, hyphens or underscores`},
			},
		},
		{
			name:  "a name longer than 32 characters",
			text:  "laptop\n" + longestName + "6\n",
			known: everyNameIsKnown,
			want: Problems{
				{Line: 2, Cause: `"` + longestName + `6" is not a valid name: use 1 to 32 letters, digits, hyphens or underscores`},
			},
		},
		{
			name:  "a name with a letter outside the allowed ones",
			text:  "# desk\n\nescritório\n",
			known: everyNameIsKnown,
			want: Problems{
				{Line: 3, Cause: `"escritório" is not a valid name: use 1 to 32 letters, digits, hyphens or underscores`},
			},
		},
		{
			name:  "a non-breaking space does not separate machines",
			text:  "laptop\u00a0desktop\n",
			known: everyNameIsKnown,
			want: Problems{
				{Line: 1, Cause: `"laptop\u00a0desktop" is not a valid name: use 1 to 32 letters, digits, hyphens or underscores`},
			},
		},
		{
			name:  "a carriage return in the middle of a line does not separate machines",
			text:  "laptop\rdesktop\n",
			known: everyNameIsKnown,
			want: Problems{
				{Line: 1, Cause: `"laptop\rdesktop" is not a valid name: use 1 to 32 letters, digits, hyphens or underscores`},
			},
		},
		{
			name:  "a name that appears twice on different lines",
			text:  "laptop desktop\n\n# shelf\nserver laptop\n",
			known: everyNameIsKnown,
			want: Problems{
				{Line: 4, Cause: `"laptop" is repeated, first seen on line 1: give each machine one place`},
			},
		},
		{
			name:  "a name that appears twice on the same line",
			text:  "# desk\nlaptop laptop\n",
			known: everyNameIsKnown,
			want: Problems{
				{Line: 2, Cause: `"laptop" is repeated, first seen on line 2: give each machine one place`},
			},
		},
		{
			name:  "a name that appears three times is reported at the second and third places",
			text:  "laptop\nlaptop\nlaptop\n",
			known: everyNameIsKnown,
			want: Problems{
				{Line: 2, Cause: `"laptop" is repeated, first seen on line 1: give each machine one place`},
				{Line: 3, Cause: `"laptop" is repeated, first seen on line 1: give each machine one place`},
			},
		},
		{
			name:  "a name that is not a known machine",
			text:  "# desk\nlaptop tablet\n",
			known: onlyKnown("laptop"),
			want: Problems{
				{Line: 2, Cause: `"tablet" is not a known machine: use the name of this machine or one that "lanhop peers" lists`},
			},
		},
		{
			name:  "a known name written in another case is not known",
			text:  "Laptop\n",
			known: onlyKnown("laptop"),
			want: Problems{
				{Line: 1, Cause: `"Laptop" is not a known machine: use the name of this machine or one that "lanhop peers" lists`},
			},
		},
		{
			name:  "an unknown name that appears twice is reported as unknown once",
			text:  "tablet\ntablet\n",
			known: onlyKnown("laptop"),
			want: Problems{
				{Line: 1, Cause: `"tablet" is not a known machine: use the name of this machine or one that "lanhop peers" lists`},
				{Line: 2, Cause: `"tablet" is repeated, first seen on line 1: give each machine one place`},
			},
		},
		{
			name:  "an empty file",
			text:  "",
			known: everyNameIsKnown,
			want: Problems{
				{Line: 0, Cause: "the layout has no machine: write the names of the machines in a row, from left to right"},
			},
		},
		{
			name:  "a file with only comments, blank lines and holes",
			text:  "# my desk\n\n. .\n   \n",
			known: everyNameIsKnown,
			want: Problems{
				{Line: 0, Cause: "the layout has no machine: write the names of the machines in a row, from left to right"},
			},
		},
		{
			name:  "a file whose only names are wrong is not also reported as empty",
			text:  "lap!top\n",
			known: everyNameIsKnown,
			want: Problems{
				{Line: 1, Cause: `"lap!top" is not a valid name: use 1 to 32 letters, digits, hyphens or underscores`},
			},
		},
		{
			name:  "several problems in one file, in the order of the file",
			text:  "# my desk\n\nlaptop  desk/top  tablet\n\n# below\r\n.  laptop  phone  ser:ver\r\n",
			known: onlyKnown("laptop", "desktop"),
			want: Problems{
				{Line: 3, Cause: `"desk/top" is not a valid name: use 1 to 32 letters, digits, hyphens or underscores`},
				{Line: 3, Cause: `"tablet" is not a known machine: use the name of this machine or one that "lanhop peers" lists`},
				{Line: 6, Cause: `"laptop" is repeated, first seen on line 3: give each machine one place`},
				{Line: 6, Cause: `"phone" is not a known machine: use the name of this machine or one that "lanhop peers" lists`},
				{Line: 6, Cause: `"ser:ver" is not a valid name: use 1 to 32 letters, digits, hyphens or underscores`},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := problemsOf(t, c.text, c.known)

			if !slices.Equal(got, c.want) {
				t.Errorf("problems:\n%v\nwant:\n%v", got, c.want)
			}
		})
	}
}

func TestProblemsReadAsOneLineEachWithTheLineOfTheFile(t *testing.T) {
	problems := problemsOf(t, "laptop\nlaptop tablet\n", onlyKnown("laptop"))

	want := `line 2: "laptop" is repeated, first seen on line 1: give each machine one place
line 2: "tablet" is not a known machine: use the name of this machine or one that "lanhop peers" lists`
	if got := problems.Error(); got != want {
		t.Errorf("Error() =\n%s\nwant:\n%s", got, want)
	}
}

func TestAProblemOfTheWholeFileReadsWithoutALine(t *testing.T) {
	problems := problemsOf(t, "# nothing yet\n", everyNameIsKnown)

	want := "the layout has no machine: write the names of the machines in a row, from left to right"
	if got := problems.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestOnlyValidNamesAreCheckedAgainstTheKnownMachines(t *testing.T) {
	var asked []string
	remember := func(name string) bool {
		asked = append(asked, name)
		return true
	}

	if _, err := Parse(strings.NewReader("laptop . desk.top\n# shelf\nServer laptop\n"), remember); err == nil {
		t.Fatal("Parse accepted a file with a wrong name and a repeated name")
	}

	if want := []string{"laptop", "Server"}; !slices.Equal(asked, want) {
		t.Errorf("names checked = %q, want %q", asked, want)
	}
}

type brokenReader struct{ failure error }

func (r brokenReader) Read([]byte) (int, error) { return 0, r.failure }

func TestParseReturnsTheErrorOfAFileThatCannotBeRead(t *testing.T) {
	failure := errors.New("the disk is gone")

	parsed, err := Parse(brokenReader{failure}, everyNameIsKnown)

	if !errors.Is(err, failure) {
		t.Errorf("Parse error = %v, want %v", err, failure)
	}
	var problems Problems
	if errors.As(err, &problems) {
		t.Errorf("Parse reported %v as problems of the file", err)
	}
	if parsed != nil {
		t.Error("Parse returned a layout from a file it could not read")
	}
}

func TestAMachineOutsideTheLayoutIsNotInItAndHasNoNeighbours(t *testing.T) {
	parsed := parse(t, planExample)

	for _, name := range []string{"tablet", "Laptop", ".", ""} {
		if parsed.Has(name) {
			t.Errorf("Has(%q) = true, want false", name)
		}
		if got := neighboursOf(t, parsed, name); got != (neighbours{}) {
			t.Errorf("neighbours of %q = %+v, want none", name, got)
		}
	}
}

func TestASideThatDoesNotExistHasNoNeighbour(t *testing.T) {
	parsed := parse(t, "a b\nc d\n")

	for _, side := range []Side{Left - 1, Below + 1} {
		if neighbour, found := parsed.Neighbour("a", side); found || neighbour != "" {
			t.Errorf("Neighbour(a, %d) = %q, %v, want no neighbour", int(side), neighbour, found)
		}
	}
}

func TestChangingTheListOfMachinesDoesNotChangeTheLayout(t *testing.T) {
	parsed := parse(t, "laptop desktop\n")

	parsed.Machines()[0] = "tablet"

	if want := []string{"laptop", "desktop"}; !slices.Equal(parsed.Machines(), want) {
		t.Errorf("Machines() = %q, want %q", parsed.Machines(), want)
	}
	if right := neighbourOn(t, parsed, "laptop", Right); right != "desktop" {
		t.Errorf("right of laptop = %q, want desktop", right)
	}
}

func TestSidesAreNamedAsThePersonSeesThem(t *testing.T) {
	names := map[Side]string{Left: "left", Right: "right", Above: "above", Below: "below", Below + 1: "Side(4)"}
	for side, want := range names {
		if got := side.String(); got != want {
			t.Errorf("Side(%d).String() = %q, want %q", int(side), got, want)
		}
	}
}

func TestDescribeDrawsTheDeskAndListsTheNeighboursOfEachMachine(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{
			name: "the example of the plan",
			text: planExample,
			want: `laptop  desktop  macbook
.       server   .

laptop: right desktop
desktop: left laptop, right macbook, below server
macbook: left desktop
server: above desktop
`,
		},
		{
			name: "a machine alone",
			text: "laptop\n",
			want: `laptop

laptop: no neighbours
`,
		},
		{
			name: "rows of different lengths, with every side in use",
			text: "\tmacbook-a  b\n# middle\nc d e   # the long row\n\n.\tserver\r\n",
			want: `macbook-a  b
c          d       e
.          server

macbook-a: right b, below c
b: left macbook-a, below d
c: right d, above macbook-a
d: left c, right e, above b, below server
e: left d
server: above d
`,
		},
		{
			name: "holes and rows of holes are drawn as written",
			text: "a . b\n.\nc\n",
			want: `a  .  b
.
c

a: no neighbours
b: no neighbours
c: no neighbours
`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parse(t, c.text).Describe(); got != c.want {
				t.Errorf("Describe() =\n%s\nwant:\n%s", got, c.want)
			}
		})
	}
}
