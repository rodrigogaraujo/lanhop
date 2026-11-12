package layoutedit

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rodrigogaraujo/lanhop/internal/layout"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

const (
	listensOn  = "127.0.0.1:43210"
	rightToken = "5f2c9a7d41e80b36c1d2e3f4a5b6c7d8"
	ownName    = "laptop"

	ownIsNotOnTheDesk = `This machine, "laptop", is not on the desk. Put it in a place, so that the edges of its screen lead somewhere.`
)

type trustedMachines struct{ names []string }

func (t *trustedMachines) Peers() []trust.Peer {
	peers := make([]trust.Peer, len(t.names))
	for i, name := range t.names {
		peers[i] = trust.Peer{Name: name}
	}
	return peers
}

type openedPage struct {
	editor  Editor
	trusted *trustedMachines
	handler http.Handler
	pressed int
}

func openPage(t *testing.T, trusted ...string) *openedPage {
	t.Helper()
	opened := &openedPage{trusted: &trustedMachines{names: trusted}}
	opened.editor = Editor{
		Path:    filepath.Join(t.TempDir(), "lanhop", "layout"),
		Own:     ownName,
		Trusted: opened.trusted,
	}
	opened.handler = opened.editor.Handler(listensOn, rightToken, func() { opened.pressed++ })
	return opened
}

func openPageOnTheDeskOfThePlan(t *testing.T) *openedPage {
	t.Helper()
	return openPage(t, "desktop", "macbook", "server")
}

func (p *openedPage) fileHolds(t *testing.T, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p.editor.Path), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(p.editor.Path, []byte(text), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func (p *openedPage) expectFile(t *testing.T, want string) {
	t.Helper()
	written, err := os.ReadFile(p.editor.Path)
	if err != nil {
		t.Fatalf("the layout file cannot be read: %v", err)
	}
	if string(written) != want {
		t.Errorf("the layout file holds:\n%s\nwant:\n%s", written, want)
	}
}

func (p *openedPage) expectNothingWritten(t *testing.T) {
	t.Helper()
	if found, _ := filepath.Glob(filepath.Join(filepath.Dir(filepath.Dir(p.editor.Path)), "*")); len(found) > 0 {
		t.Errorf("files were written although nothing was saved: %q", found)
	}
}

func (p *openedPage) expectOnlyTheLayoutFile(t *testing.T) {
	t.Helper()
	found, err := filepath.Glob(filepath.Join(filepath.Dir(p.editor.Path), "*"))
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if !slices.Equal(found, []string{p.editor.Path}) {
		t.Errorf("the directory of the layout file holds %q, want only %q", found, p.editor.Path)
	}
}

type sent struct {
	method string
	path   string
	query  string
	host   string
	body   string
}

func (p *openedPage) send(request sent) *httptest.ResponseRecorder {
	built := httptest.NewRequest(request.method, "http://"+listensOn+request.path+request.query, strings.NewReader(request.body))
	built.Host = request.host
	response := httptest.NewRecorder()
	p.handler.ServeHTTP(response, built)
	return response
}

func (p *openedPage) ask(method, path, body string) *httptest.ResponseRecorder {
	return p.send(sent{method: method, path: path, query: "?token=" + rightToken, host: listensOn, body: body})
}

func arranged(t *testing.T, rows [][]string) string {
	t.Helper()
	body, err := json.Marshal(arrangement{Rows: rows})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return string(body)
}

func answered[body any](t *testing.T, response *httptest.ResponseRecorder, status int) body {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, want %d, with body %q", response.Code, status, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	var decoded body
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("the answer %q is not JSON: %v", response.Body.String(), err)
	}
	return decoded
}

var everyRequest = []sent{
	{method: http.MethodGet, path: "/"},
	{method: http.MethodGet, path: "/desk"},
	{method: http.MethodPost, path: "/arrange", body: `{"rows":[["laptop"]]}`},
	{method: http.MethodPost, path: "/save", body: `{"rows":[["laptop"]]}`},
	{method: http.MethodPost, path: "/done"},
}

func TestEveryRequestIsRefusedWithoutTheTokenOfTheAddress(t *testing.T) {
	queries := map[string]string{
		"no token":                         "",
		"an empty token":                   "?token=",
		"another token of the same length": "?token=" + strings.Repeat("0", len(rightToken)),
		"the start of the token":           "?token=" + rightToken[:len(rightToken)-1],
		"the token and more":               "?token=" + rightToken + "0",
		"the token in capital letters":     "?token=" + strings.ToUpper(rightToken),
		"the token under another name":     "?key=" + rightToken,
	}
	for name, query := range queries {
		for _, request := range everyRequest {
			t.Run(name+" on "+request.path, func(t *testing.T) {
				opened := openPage(t)
				request.query, request.host = query, listensOn

				response := opened.send(request)

				if response.Code != http.StatusForbidden {
					t.Errorf("status = %d, want %d", response.Code, http.StatusForbidden)
				}
				if strings.Contains(response.Body.String(), ownName) || strings.Contains(response.Body.String(), "<") {
					t.Errorf("the refusal shows the desk or the page: %q", response.Body.String())
				}
				if opened.pressed != 0 {
					t.Error("the editor was told to stop")
				}
				opened.expectNothingWritten(t)
			})
		}
	}
}

func TestEveryRequestIsRefusedWhenItsHostIsNotTheAddressLanhopListensOn(t *testing.T) {
	hosts := map[string]string{
		"a name that resolves to this machine": "localhost:43210",
		"another site":                         "evil.example:43210",
		"another site without a port":          "evil.example",
		"the address without the port":         "127.0.0.1",
		"another port":                         "127.0.0.1:43211",
		"the address inside another name":      "127.0.0.1:43210.evil.example",
		"no host at all":                       "",
	}
	for name, host := range hosts {
		for _, request := range everyRequest {
			t.Run(name+" on "+request.path, func(t *testing.T) {
				opened := openPage(t)
				request.query, request.host = "?token="+rightToken, host

				response := opened.send(request)

				if response.Code != http.StatusForbidden {
					t.Errorf("status = %d, want %d", response.Code, http.StatusForbidden)
				}
				if opened.pressed != 0 {
					t.Error("the editor was told to stop")
				}
				opened.expectNothingWritten(t)
			})
		}
	}
}

func TestARequestWithTheWrongMethodDoesNothing(t *testing.T) {
	requests := []sent{
		{method: http.MethodGet, path: "/arrange"},
		{method: http.MethodGet, path: "/save"},
		{method: http.MethodGet, path: "/done"},
		{method: http.MethodPut, path: "/save", body: `{"rows":[["laptop"]]}`},
		{method: http.MethodDelete, path: "/desk"},
		{method: http.MethodPost, path: "/desk", body: `{"rows":[["laptop"]]}`},
		{method: http.MethodPost, path: "/", body: `{"rows":[["laptop"]]}`},
	}
	for _, request := range requests {
		t.Run(request.method+" "+request.path, func(t *testing.T) {
			opened := openPage(t)

			response := opened.ask(request.method, request.path, request.body)

			if response.Code != http.StatusMethodNotAllowed {
				t.Errorf("status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
			}
			if opened.pressed != 0 {
				t.Error("the editor was told to stop")
			}
			opened.expectNothingWritten(t)
		})
	}
}

func TestAnAddressTheEditorDoesNotHaveIsNotFound(t *testing.T) {
	opened := openPage(t)

	for _, path := range []string{"/layout", "/desk/", "/page.html", "/favicon.ico"} {
		if response := opened.ask(http.MethodGet, path, ""); response.Code != http.StatusNotFound {
			t.Errorf("GET %s: status = %d, want %d", path, response.Code, http.StatusNotFound)
		}
	}
}

func TestThePageIsServedAtTheAddressWithTheToken(t *testing.T) {
	opened := openPage(t)

	response := opened.ask(http.MethodGet, "/", "")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/html; charset=utf-8", got)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if !strings.HasPrefix(response.Body.String(), "<!doctype html>") {
		t.Errorf("the page starts with %.40q, want the doctype of an HTML page", response.Body.String())
	}
}

func TestThePageLoadsNothingFromOutsideItself(t *testing.T) {
	served := openPage(t).ask(http.MethodGet, "/", "").Body.String()

	for _, reference := range []string{"http:", "https:", "//", "src=", "href=", "url(", "@import", "<link", "<img", "<iframe", "import("} {
		if strings.Contains(served, reference) {
			t.Errorf("the page has %q, so it may load something from outside itself", reference)
		}
	}
}

func TestThePageSaysWhatSavingDoes(t *testing.T) {
	served := openPage(t).ask(http.MethodGet, "/", "").Body.String()

	for _, said := range []string{
		`<p id="comments" hidden>The layout file has comments. Saving does not keep them.</p>`,
		`A running "lanhop share" uses the new layout at once, with no restart.`,
		`<button id="save" type="button" disabled>Save</button>`,
		`<button id="done" type="button">Done</button>`,
	} {
		if !strings.Contains(served, said) {
			t.Errorf("the page lacks %q", said)
		}
	}
}

func TestTheDeskStartsEmptyWithEveryMachineWaitingWhenThereIsNoLayoutFile(t *testing.T) {
	opened := openPageOnTheDeskOfThePlan(t)

	shown := answered[desk](t, opened.ask(http.MethodGet, "/desk", ""), http.StatusOK)

	if shown.Own != ownName {
		t.Errorf("this machine is %q, want %q", shown.Own, ownName)
	}
	if want := []string{"laptop", "desktop", "macbook", "server"}; !slices.Equal(shown.Machines, want) {
		t.Errorf("machines = %q, want %q", shown.Machines, want)
	}
	if !sameRows(shown.Rows, emptyRows(3, 3)) {
		t.Errorf("rows = %q, want three empty rows of three", shown.Rows)
	}
	if shown.Comments {
		t.Error("the desk says the file has comments, and there is no file")
	}
	if shown.Problem != ownIsNotOnTheDesk || len(shown.Neighbours) != 0 {
		t.Errorf("problem = %q, neighbours = %q, want only the problem %q", shown.Problem, shown.Neighbours, ownIsNotOnTheDesk)
	}
	opened.expectNothingWritten(t)
}

func TestTheDeskShowsTheMachinesOfTheLayoutFileInTheirPlacesAndTheirNeighbours(t *testing.T) {
	opened := openPage(t, "desktop", "macbook", "server", "tablet")
	opened.fileHolds(t, "laptop   desktop   macbook\n.        server    .\n")

	shown := answered[desk](t, opened.ask(http.MethodGet, "/desk", ""), http.StatusOK)

	wantRows := [][]string{
		{"", "", "", "", ""},
		{"", "laptop", "desktop", "macbook", ""},
		{"", "", "server", "", ""},
		{"", "", "", "", ""},
	}
	if !sameRows(shown.Rows, wantRows) {
		t.Errorf("rows = %q, want %q", shown.Rows, wantRows)
	}
	if want := []string{"laptop", "desktop", "macbook", "server", "tablet"}; !slices.Equal(shown.Machines, want) {
		t.Errorf("machines = %q, want %q", shown.Machines, want)
	}
	wantNeighbours := []string{
		"laptop (this machine): right desktop",
		"desktop: left laptop, right macbook, below server",
		"macbook: left desktop",
		"server: above desktop",
	}
	if !slices.Equal(shown.Neighbours, wantNeighbours) {
		t.Errorf("neighbours = %q, want %q", shown.Neighbours, wantNeighbours)
	}
	if shown.Problem != "" {
		t.Errorf("problem = %q, want none", shown.Problem)
	}
	if shown.Comments {
		t.Error("the desk says the file has comments, and it has none")
	}
}

func TestTheDeskSaysWhenTheLayoutFileHasComments(t *testing.T) {
	for _, text := range []string{"# my desk\nlaptop desktop\n", "laptop desktop # the long table\n", "laptop\n#\n"} {
		opened := openPageOnTheDeskOfThePlan(t)
		opened.fileHolds(t, text)

		shown := answered[desk](t, opened.ask(http.MethodGet, "/desk", ""), http.StatusOK)

		if !shown.Comments {
			t.Errorf("the desk does not say that the file %q has comments", text)
		}
	}
}

func TestSavingLeavesNoCommentsToWarnAbout(t *testing.T) {
	opened := openPageOnTheDeskOfThePlan(t)
	opened.fileHolds(t, "# my desk\nlaptop desktop\n")
	shown := answered[desk](t, opened.ask(http.MethodGet, "/desk", ""), http.StatusOK)

	answered[struct{}](t, opened.ask(http.MethodPost, "/save", arranged(t, shown.Rows)), http.StatusOK)

	opened.expectFile(t, "laptop  desktop\n")
	if again := answered[desk](t, opened.ask(http.MethodGet, "/desk", ""), http.StatusOK); again.Comments {
		t.Error("the desk still says the file has comments after a save")
	}
}

func TestTheDeskOfALayoutFileWithoutThisMachineShowsItWaitingAndSaysSo(t *testing.T) {
	opened := openPageOnTheDeskOfThePlan(t)
	opened.fileHolds(t, "desktop macbook\n")

	shown := answered[desk](t, opened.ask(http.MethodGet, "/desk", ""), http.StatusOK)

	if want := [][]string{{"", "", "", ""}, {"", "desktop", "macbook", ""}, {"", "", "", ""}}; !sameRows(shown.Rows, want) {
		t.Errorf("rows = %q, want %q", shown.Rows, want)
	}
	if shown.Problem != ownIsNotOnTheDesk {
		t.Errorf("problem = %q, want %q", shown.Problem, ownIsNotOnTheDesk)
	}
}

func TestALayoutFileWithProblemsIsNotShownAsADesk(t *testing.T) {
	opened := openPageOnTheDeskOfThePlan(t)
	opened.fileHolds(t, "laptop tablet\nlaptop\n")

	refused := answered[map[string]any](t, opened.ask(http.MethodGet, "/desk", ""), http.StatusConflict)

	want := `The layout file has problems, so it cannot be shown here:
line 1: "tablet" is not a known machine: use the name of this machine or one that "lanhop peers" lists
line 2: "laptop" is repeated, first seen on line 1: give each machine one place
Fix those lines by hand, or remove the file, and load this page again.`
	if refused["problem"] != want {
		t.Errorf("problem = %q, want %q", refused["problem"], want)
	}
	if _, shown := refused["rows"]; shown {
		t.Errorf("the answer has rows: %v", refused)
	}
	opened.expectFile(t, "laptop tablet\nlaptop\n")
}

func TestAMachineTrustedUnderTheNameOfThisMachineIsNotASecondMachine(t *testing.T) {
	opened := openPage(t, "desktop", ownName, "server")

	shown := answered[desk](t, opened.ask(http.MethodGet, "/desk", ""), http.StatusOK)

	if want := []string{"laptop", "desktop", "server"}; !slices.Equal(shown.Machines, want) {
		t.Errorf("machines = %q, want %q", shown.Machines, want)
	}
}

func TestArrangingShowsTheGridGrownAroundTheMachinesAndTheNeighboursInWords(t *testing.T) {
	opened := openPageOnTheDeskOfThePlan(t)
	moved := [][]string{
		{"", "", "server"},
		{"", "laptop", ""},
		{"desktop", "", ""},
	}

	seen := answered[view](t, opened.ask(http.MethodPost, "/arrange", arranged(t, moved)), http.StatusOK)

	wantRows := [][]string{
		{"", "", "", "", ""},
		{"", "", "", "server", ""},
		{"", "", "laptop", "", ""},
		{"", "desktop", "", "", ""},
		{"", "", "", "", ""},
	}
	if !sameRows(seen.Rows, wantRows) {
		t.Errorf("rows = %q, want %q", seen.Rows, wantRows)
	}
	want := []string{"server: no neighbours", "laptop (this machine): no neighbours", "desktop: no neighbours"}
	if !slices.Equal(seen.Neighbours, want) || seen.Problem != "" {
		t.Errorf("neighbours = %q, problem = %q, want %q and no problem", seen.Neighbours, seen.Problem, want)
	}
	opened.expectNothingWritten(t)
}

func TestTheNeighboursFollowAMachineThatMoves(t *testing.T) {
	opened := openPageOnTheDeskOfThePlan(t)
	steps := []struct {
		rows [][]string
		want []string
	}{
		{
			rows: [][]string{{"laptop", "desktop"}},
			want: []string{"laptop (this machine): right desktop", "desktop: left laptop"},
		},
		{
			rows: [][]string{{"desktop", "laptop"}},
			want: []string{"desktop: right laptop", "laptop (this machine): left desktop"},
		},
		{
			rows: [][]string{{"desktop"}, {"laptop"}},
			want: []string{"desktop: below laptop", "laptop (this machine): above desktop"},
		},
		{
			rows: [][]string{{"", "laptop"}},
			want: []string{"laptop (this machine): no neighbours"},
		},
	}
	for _, step := range steps {
		seen := answered[view](t, opened.ask(http.MethodPost, "/arrange", arranged(t, step.rows)), http.StatusOK)

		if !slices.Equal(seen.Neighbours, step.want) || seen.Problem != "" {
			t.Errorf("with %q, neighbours = %q and problem = %q, want %q and no problem", step.rows, seen.Neighbours, seen.Problem, step.want)
		}
	}
}

var arrangementsThatCannotBeSaved = []struct {
	name    string
	rows    [][]string
	problem string
}{
	{
		name:    "this machine is not placed",
		rows:    [][]string{{"", "desktop", "macbook"}},
		problem: ownIsNotOnTheDesk,
	},
	{
		name:    "no machine is placed",
		rows:    [][]string{{"", ""}, {"", ""}},
		problem: ownIsNotOnTheDesk,
	},
	{
		name:    "no row is sent",
		rows:    nil,
		problem: ownIsNotOnTheDesk,
	},
	{
		name:    "a machine that is not trusted",
		rows:    [][]string{{"laptop", "tablet"}},
		problem: `line 1: "tablet" is not a known machine: use the name of this machine or one that "lanhop peers" lists`,
	},
	{
		name:    "a trusted machine written in another case",
		rows:    [][]string{{"laptop", "Desktop"}},
		problem: `line 1: "Desktop" is not a known machine: use the name of this machine or one that "lanhop peers" lists`,
	},
	{
		name:    "a machine placed twice",
		rows:    [][]string{{"laptop", "desktop"}, {"desktop", ""}},
		problem: `line 2: "desktop" is repeated, first seen on line 1: give each machine one place`,
	},
	{
		name:    "this machine placed twice",
		rows:    [][]string{{"laptop", "", "laptop"}},
		problem: `line 1: "laptop" is repeated, first seen on line 1: give each machine one place`,
	},
	{
		name:    "two trusted names in one place",
		rows:    [][]string{{"laptop", "desktop macbook"}},
		problem: `"desktop macbook" is not the name of a machine.`,
	},
	{
		name:    "a name that would start a second row",
		rows:    [][]string{{"laptop", "desktop\nmacbook"}},
		problem: `"desktop\nmacbook" is not the name of a machine.`,
	},
	{
		name:    "a name that would turn the rest of the row into a comment",
		rows:    [][]string{{"#", "laptop"}},
		problem: `"#" is not the name of a machine.`,
	},
	{
		name:    "a dot sent as a name",
		rows:    [][]string{{"laptop", ".", "desktop"}},
		problem: `"." is not the name of a machine.`,
	},
	{
		name:    "a name of spaces",
		rows:    [][]string{{"laptop", "  "}},
		problem: `"  " is not the name of a machine.`,
	},
	{
		name:    "a name longer than a machine may have",
		rows:    [][]string{{"laptop", strings.Repeat("a", trust.MaxNameLength+1)}},
		problem: `"` + strings.Repeat("a", trust.MaxNameLength+1) + `" is not the name of a machine.`,
	},
}

func TestArrangingSaysWhyAnArrangementCannotBeSaved(t *testing.T) {
	for _, c := range arrangementsThatCannotBeSaved {
		t.Run(c.name, func(t *testing.T) {
			opened := openPageOnTheDeskOfThePlan(t)

			seen := answered[view](t, opened.ask(http.MethodPost, "/arrange", arranged(t, c.rows)), http.StatusOK)

			if seen.Problem != c.problem {
				t.Errorf("problem = %q, want %q", seen.Problem, c.problem)
			}
			if seen.Neighbours == nil || len(seen.Neighbours) != 0 {
				t.Errorf("neighbours = %#v, want an empty list", seen.Neighbours)
			}
			if !sameRows(seen.Rows, framed(c.rows)) {
				t.Errorf("rows = %q, want %q", seen.Rows, framed(c.rows))
			}
			opened.expectNothingWritten(t)
		})
	}
}

func TestSavingAnArrangementThatCannotBeSavedWritesNothing(t *testing.T) {
	for _, c := range arrangementsThatCannotBeSaved {
		t.Run(c.name+", with no file yet", func(t *testing.T) {
			opened := openPageOnTheDeskOfThePlan(t)

			refused := answered[trouble](t, opened.ask(http.MethodPost, "/save", arranged(t, c.rows)), http.StatusUnprocessableEntity)

			if refused.Problem != c.problem {
				t.Errorf("problem = %q, want %q", refused.Problem, c.problem)
			}
			opened.expectNothingWritten(t)
		})
		t.Run(c.name+", with a file in use", func(t *testing.T) {
			opened := openPageOnTheDeskOfThePlan(t)
			opened.fileHolds(t, "# in use\nlaptop server\n")

			refused := answered[trouble](t, opened.ask(http.MethodPost, "/save", arranged(t, c.rows)), http.StatusUnprocessableEntity)

			if refused.Problem != c.problem {
				t.Errorf("problem = %q, want %q", refused.Problem, c.problem)
			}
			opened.expectFile(t, "# in use\nlaptop server\n")
			opened.expectOnlyTheLayoutFile(t)
		})
	}
}

func TestARequestThatIsNotAnArrangementIsRefused(t *testing.T) {
	bodies := map[string]string{
		"nothing":                 "",
		"text":                    "laptop desktop",
		"rows that are text":      `{"rows":"laptop desktop"}`,
		"places that are numbers": `{"rows":[[1,2]]}`,
		"JSON cut short":          `{"rows":[["laptop"`,
	}
	for name, body := range bodies {
		for _, path := range []string{"/arrange", "/save"} {
			t.Run(name+" on "+path, func(t *testing.T) {
				opened := openPageOnTheDeskOfThePlan(t)

				refused := answered[trouble](t, opened.ask(http.MethodPost, path, body), http.StatusBadRequest)

				if refused.Problem != "The request is not an arrangement of the desk." {
					t.Errorf("problem = %q", refused.Problem)
				}
				opened.expectNothingWritten(t)
			})
		}
	}
}

func TestSavingWritesTheRowsFromTopToBottomWithoutTheEmptyRowsAndColumnsAround(t *testing.T) {
	cases := []struct {
		name string
		rows [][]string
		want string
	}{
		{
			name: "this machine alone",
			rows: [][]string{{"", "", ""}, {"", "laptop", ""}, {"", "", ""}},
			want: "laptop\n",
		},
		{
			name: "the desk of the plan with the empty rows and columns the page shows",
			rows: [][]string{
				{"", "", "", "", ""},
				{"", "laptop", "desktop", "macbook", ""},
				{"", "", "server", "", ""},
				{"", "", "", "", ""},
			},
			want: "laptop  desktop  macbook\n.       server   .\n",
		},
		{
			name: "a column",
			rows: [][]string{{"", "server", ""}, {"", "laptop", ""}, {"", "desktop", ""}},
			want: "server\nlaptop\ndesktop\n",
		},
		{
			name: "an empty place between two machines is a dot",
			rows: [][]string{{"macbook", "", "laptop"}},
			want: "macbook  .  laptop\n",
		},
		{
			name: "an empty row between two machines is a row of dots",
			rows: [][]string{{"", ""}, {"desktop", "macbook"}, {"", ""}, {"", "laptop"}, {"", ""}},
			want: "desktop  macbook\n.        .\n.        laptop\n",
		},
		{
			name: "machines on a diagonal",
			rows: [][]string{{"", "", "", ""}, {"", "", "", "server"}, {"", "", "laptop", ""}, {"", "desktop", "", ""}},
			want: ".        .       server\n.        laptop  .\ndesktop  .       .\n",
		},
		{
			name: "rows of different lengths",
			rows: [][]string{{"", "laptop", "desktop", "macbook"}, {"", "server"}},
			want: "laptop  desktop  macbook\nserver  .        .\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			opened := openPageOnTheDeskOfThePlan(t)

			answered[struct{}](t, opened.ask(http.MethodPost, "/save", arranged(t, c.rows)), http.StatusOK)

			opened.expectFile(t, c.want)
			opened.expectOnlyTheLayoutFile(t)
			read, err := opened.editor.Read()
			if err != nil {
				t.Fatalf("the layout parser refuses the saved file: %v", err)
			}
			if !sameRows(read.Rows(), trimmed(c.rows)) {
				t.Errorf("the saved file reads back as %q, want %q", read.Rows(), trimmed(c.rows))
			}
			shown := answered[desk](t, opened.ask(http.MethodGet, "/desk", ""), http.StatusOK)
			if !sameRows(shown.Rows, framed(c.rows)) {
				t.Errorf("after the save the desk shows %q, want %q", shown.Rows, framed(c.rows))
			}
		})
	}
}

func TestSavingReplacesTheLayoutFileInOneStep(t *testing.T) {
	opened := openPageOnTheDeskOfThePlan(t)
	opened.fileHolds(t, "# before\nlaptop desktop\n")
	before := filepath.Join(t.TempDir(), "the file as it was")
	if err := os.Link(opened.editor.Path, before); err != nil {
		t.Fatalf("Link: %v", err)
	}

	answered[struct{}](t, opened.ask(http.MethodPost, "/save", arranged(t, [][]string{{"desktop", "laptop"}})), http.StatusOK)

	opened.expectFile(t, "desktop  laptop\n")
	opened.expectOnlyTheLayoutFile(t)
	kept, err := os.ReadFile(before)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(kept) != "# before\nlaptop desktop\n" {
		t.Errorf("the file that was there was written over, it now holds %q: a reader could have seen it half written", kept)
	}
}

func TestSavingCreatesTheDirectoryOfTheLayoutFileForItsOwnerOnly(t *testing.T) {
	opened := openPageOnTheDeskOfThePlan(t)

	answered[struct{}](t, opened.ask(http.MethodPost, "/save", arranged(t, [][]string{{"laptop", "server"}})), http.StatusOK)

	opened.expectFile(t, "laptop  server\n")
	directory, err := os.Stat(filepath.Dir(opened.editor.Path))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if directory.Mode().Perm() != 0o700 {
		t.Errorf("the directory has mode %o, want 700", directory.Mode().Perm())
	}
	file, err := os.Stat(opened.editor.Path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if file.Mode().Perm() != 0o600 {
		t.Errorf("the layout file has mode %o, want 600", file.Mode().Perm())
	}
}

func TestSavingSaysWhenTheLayoutFileCannotBeWritten(t *testing.T) {
	opened := openPageOnTheDeskOfThePlan(t)
	inTheWay := filepath.Dir(opened.editor.Path)
	if err := os.WriteFile(inTheWay, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	refused := answered[trouble](t, opened.ask(http.MethodPost, "/save", arranged(t, [][]string{{"laptop"}})), http.StatusInternalServerError)

	if !strings.Contains(refused.Problem, inTheWay) {
		t.Errorf("problem = %q, want it to name %q", refused.Problem, inTheWay)
	}
}

func TestSavingLeavesNoOtherFileBehindWhenTheLayoutFileCannotBeReplaced(t *testing.T) {
	opened := openPageOnTheDeskOfThePlan(t)
	if err := os.MkdirAll(filepath.Join(opened.editor.Path, "a directory where the file goes"), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	refused := answered[trouble](t, opened.ask(http.MethodPost, "/save", arranged(t, [][]string{{"laptop"}})), http.StatusInternalServerError)

	if !strings.Contains(refused.Problem, opened.editor.Path) {
		t.Errorf("problem = %q, want it to name %q", refused.Problem, opened.editor.Path)
	}
	opened.expectOnlyTheLayoutFile(t)
}

func TestAMachineThatStopsBeingTrustedCanNoLongerBeSaved(t *testing.T) {
	opened := openPageOnTheDeskOfThePlan(t)
	rows := [][]string{{"laptop", "desktop"}}
	answered[struct{}](t, opened.ask(http.MethodPost, "/save", arranged(t, rows)), http.StatusOK)

	opened.trusted.names = []string{"macbook"}
	response := opened.ask(http.MethodPost, "/save", arranged(t, [][]string{{"desktop", "laptop"}}))

	refused := answered[trouble](t, response, http.StatusUnprocessableEntity)
	if !strings.Contains(refused.Problem, `"desktop" is not a known machine`) {
		t.Errorf("problem = %q, want it to say that desktop is not a known machine", refused.Problem)
	}
	opened.expectFile(t, "laptop  desktop\n")
}

func TestDoneTellsTheEditorToStop(t *testing.T) {
	opened := openPage(t)

	answered[struct{}](t, opened.ask(http.MethodPost, "/done", ""), http.StatusOK)

	if opened.pressed != 1 {
		t.Errorf("the editor was told to stop %d times, want once", opened.pressed)
	}
	opened.expectNothingWritten(t)
}

func TestEverySavedDeskOfThreeMachinesIsReadBackAsItWasArranged(t *testing.T) {
	opened := openPage(t, "desk-top", "mac_3")
	for _, rows := range everyDeskOfThreeMachinesOnThreeByThree() {
		answered[struct{}](t, opened.ask(http.MethodPost, "/save", arranged(t, framed(rows))), http.StatusOK)

		written, err := os.ReadFile(opened.editor.Path)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		read, err := layout.Parse(strings.NewReader(string(written)), func(string) bool { return true })
		if err != nil {
			t.Fatalf("the desk %q was saved as\n%s\nwhich the parser refuses: %v", rows, written, err)
		}
		if !sameRows(read.Rows(), trimmed(rows)) {
			t.Fatalf("the desk %q was saved as\n%s\nand read back as %q", rows, written, read.Rows())
		}
	}
}
