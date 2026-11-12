package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/layout"
	"github.com/rodrigogaraujo/lanhop/internal/pairing"
)

var (
	addressOfTheEditor = regexp.MustCompile(`^http://127\.0\.0\.1:[1-9][0-9]*/\?token=[0-9a-f]{32}$`)
	errNoBrowserHere   = errors.New("no browser in this test")
)

func init() {
	openInBrowser = func(context.Context, string) error {
		return errors.New("a test must not open a real browser")
	}
}

func browserThat(t *testing.T, failure error) (opened chan string) {
	t.Helper()
	before := openInBrowser
	opened = make(chan string, 1)
	openInBrowser = func(_ context.Context, address string) error {
		opened <- address
		return failure
	}
	t.Cleanup(func() { openInBrowser = before })
	return opened
}

func addressOpenedIn(t *testing.T, opened chan string) string {
	t.Helper()
	select {
	case address := <-opened:
		return address
	case <-time.After(testPatience):
		t.Fatal("the editor never opened the browser")
		return ""
	}
}

func expectNeverOpened(t *testing.T, opened chan string) {
	t.Helper()
	select {
	case address := <-opened:
		t.Errorf("the browser was opened at %s", address)
	default:
	}
}

type runningEditor struct {
	address  string
	output   *screen
	stop     context.CancelFunc
	finished chan error
}

func startEditor(t *testing.T, browserFailure error) runningEditor {
	t.Helper()
	opened := browserThat(t, browserFailure)
	interrupted, stop := context.WithCancel(context.Background())
	started := runningEditor{output: &screen{}, stop: stop, finished: make(chan error, 1)}
	go func() { started.finished <- editLayoutUntil(interrupted, started.output) }()
	t.Cleanup(stop)
	started.address = addressOpenedIn(t, opened)
	return started
}

func (e runningEditor) at(t *testing.T, path string) string {
	t.Helper()
	address, err := url.Parse(e.address)
	if err != nil {
		t.Fatalf("the address %q cannot be read: %v", e.address, err)
	}
	address.Path = path
	return address.String()
}

func oneRequest() *http.Client {
	return &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: testPatience}
}

func get(t *testing.T, address string) (status int, body string) {
	t.Helper()
	response, err := oneRequest().Get(address)
	if err != nil {
		t.Fatalf("GET %s: %v", address, err)
	}
	defer response.Body.Close()
	read, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("GET %s: %v", address, err)
	}
	return response.StatusCode, string(read)
}

func post(t *testing.T, address string, body any) int {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	response, err := oneRequest().Post(address, "application/json", bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("POST %s: %v", address, err)
	}
	defer response.Body.Close()
	return response.StatusCode
}

func expectNoLongerServed(t *testing.T, address string) {
	t.Helper()
	response, err := oneRequest().Get(address)
	if err == nil {
		response.Body.Close()
		t.Errorf("%s still answers, with status %d, after the editor stopped", address, response.StatusCode)
	}
}

func (e runningEditor) expectStopped(t *testing.T) {
	t.Helper()
	select {
	case err := <-e.finished:
		if err != nil {
			t.Errorf("the editor stopped with %v, want no error", err)
		}
	case <-time.After(testPatience):
		t.Fatal("the editor is still running")
	}
	expectNoLongerServed(t, e.address)
}

func runLayoutEditThatMustStopByItself(t *testing.T, args ...string) (exitCode int, stdout, stderr string) {
	t.Helper()
	out, problems := &screen{}, &screen{}
	exited := make(chan int, 1)
	go func() { exited <- run(append([]string{"layout"}, args...), nobodyTyping(), out, problems) }()
	select {
	case exitCode = <-exited:
	case <-time.After(testPatience):
		t.Fatalf("lanhop layout %q is still running, after it printed %q", args, out)
	}
	return exitCode, out.String(), problems.String()
}

func TestLayoutEditServesThePageAtTheAddressItPrintsUntilDoneIsPressed(t *testing.T) {
	directory := useConfigDirectory(t)
	opened := browserThat(t, nil)
	stdout, stderr := &screen{}, &screen{}
	exited := make(chan int, 1)
	go func() { exited <- run([]string{"layout", "--edit"}, nobodyTyping(), stdout, stderr) }()
	editor := runningEditor{address: addressOpenedIn(t, opened)}

	if !addressOfTheEditor.MatchString(editor.address) {
		t.Fatalf("the browser was opened at %q, want an address of this machine alone with a token", editor.address)
	}
	expectShown(t, stdout,
		"The layout file is "+filepath.Join(directory, layoutFileName)+"\n",
		"Arrange the desk at "+editor.address+"\n",
		"Press Done on the page, or Ctrl+C here, to stop.",
	)
	expectNotShown(t, stdout, "could not open a browser")
	if status, page := get(t, editor.address); status != http.StatusOK || !strings.Contains(page, "<title>lanhop layout</title>") {
		t.Errorf("GET %s: status %d and %.80q, want the page of the editor", editor.address, status, page)
	}
	select {
	case code := <-exited:
		t.Fatalf("the command stopped with %d before Done was pressed, stderr = %q", code, stderr)
	default:
	}

	if status := post(t, editor.at(t, "/done"), struct{}{}); status != http.StatusOK {
		t.Errorf("POST /done: status %d, want %d", status, http.StatusOK)
	}

	select {
	case code := <-exited:
		if code != 0 {
			t.Errorf("exit code = %d, stderr = %q", code, stderr)
		}
	case <-time.After(testPatience):
		t.Fatal("the command kept running after Done was pressed")
	}
	expectNoLongerServed(t, editor.address)
}

func TestLayoutEditStopsServingWhenItIsInterrupted(t *testing.T) {
	useConfigDirectory(t)
	editor := startEditor(t, nil)
	if status, _ := get(t, editor.address); status != http.StatusOK {
		t.Fatalf("GET %s: status %d, want %d", editor.address, status, http.StatusOK)
	}

	editor.stop()

	editor.expectStopped(t)
}

func TestLayoutEditRefusesAnAddressWithoutItsToken(t *testing.T) {
	useConfigDirectory(t)
	editor := startEditor(t, nil)
	withoutToken, _, _ := strings.Cut(editor.address, "?")

	for _, address := range []string{withoutToken, withoutToken + "?token=" + strings.Repeat("0", 32), withoutToken + "desk"} {
		if status, body := get(t, address); status != http.StatusForbidden || strings.Contains(body, "<") {
			t.Errorf("GET %s: status %d and %.80q, want %d and no page", address, status, body, http.StatusForbidden)
		}
	}
}

func TestLayoutEditGivesEachRunItsOwnToken(t *testing.T) {
	useConfigDirectory(t)
	first := startEditor(t, nil)
	first.stop()
	first.expectStopped(t)
	second := startEditor(t, nil)

	if !addressOfTheEditor.MatchString(second.address) {
		t.Fatalf("the browser was opened at %q, want an address of this machine alone with a token", second.address)
	}
	_, firstToken, _ := strings.Cut(first.address, "?")
	_, secondToken, _ := strings.Cut(second.address, "?")
	if firstToken == secondToken {
		t.Errorf("two runs used the same token: %s and %s", first.address, second.address)
	}
}

func TestLayoutEditSavesTheLayoutFileThatShareFollowsAndLayoutPrints(t *testing.T) {
	directory := useConfigDirectory(t)
	trustAlready(t, directory, deskNextDoor, laptopNextDoor)
	own := pairing.OwnName()
	peersPath := filepath.Join(directory, peersFileName)
	following := &deskOnDisk{
		path:      filepath.Join(directory, layoutFileName),
		peersPath: peersPath,
		own:       own,
		known:     knownOnThisDesk(own, peersOnDisk(peersPath)),
		ownEdges:  true,
		stdout:    &screen{},
	}
	following.start()
	editor := startEditor(t, nil)

	arrangement := map[string][][]string{"rows": {{"", "", ""}, {"", deskNextDoor.Name, own}, {"", "", laptopNextDoor.Name}}}
	if status := post(t, editor.at(t, "/save"), arrangement); status != http.StatusOK {
		t.Fatalf("POST /save: status %d, want %d", status, http.StatusOK)
	}

	following.refresh()
	desk := following.Layout()
	if desk == nil {
		t.Fatal("share uses no layout after the save")
	}
	for side, want := range map[layout.Side]string{layout.Left: deskNextDoor.Name, layout.Below: laptopNextDoor.Name, layout.Right: "", layout.Above: ""} {
		if got, _ := desk.Neighbour(own, side); got != want {
			t.Errorf("after the save share has %q on the %s of this machine, want %q", got, side, want)
		}
	}
	exitCode, stdout, stderr := runLayout(t)
	wanted := fmt.Sprintf("%s (this machine): left %s, below %s\n", own, deskNextDoor.Name, laptopNextDoor.Name)
	if exitCode != 0 || !strings.Contains(stdout, wanted) {
		t.Errorf("lanhop layout: exit code %d, stderr %q and stdout %q, which lacks %q", exitCode, stderr, stdout, wanted)
	}
}

func TestLayoutEditRefusesToChangeTheLayoutFromAnArrangementWithAnUnknownMachine(t *testing.T) {
	directory := useConfigDirectory(t)
	file := layoutFileIn(directory)
	file.save(t, pairing.OwnName()+"\n")
	editor := startEditor(t, nil)

	arrangement := map[string][][]string{"rows": {{pairing.OwnName(), deskNextDoor.Name}}}
	if status := post(t, editor.at(t, "/save"), arrangement); status != http.StatusUnprocessableEntity {
		t.Errorf("POST /save: status %d, want %d", status, http.StatusUnprocessableEntity)
	}

	if written, err := os.ReadFile(file.path); err != nil || string(written) != pairing.OwnName()+"\n" {
		t.Errorf("the layout file holds %q (%v), want it as it was", written, err)
	}
}

func TestLayoutEditSaysPlainlyWhenNoBrowserOpensAndHowToEditByHand(t *testing.T) {
	useConfigDirectory(t)
	editor := startEditor(t, errNoBrowserHere)

	editor.output.waitUntilShown(t, "for an empty place.\n")
	expectShown(t, editor.output,
		"Arrange the desk at "+editor.address+"\n",
		"lanhop could not open a browser on this machine: no browser in this test.\n",
		"Open the address above in a browser on this machine.",
		"stop with Ctrl+C",
		"edit the layout file by hand: each line is a row of machines, from left to right, with a dot\nfor an empty place.",
	)
	if status, _ := get(t, editor.address); status != http.StatusOK {
		t.Errorf("GET %s: status %d, want the page to be served for a browser opened by hand", editor.address, status)
	}

	editor.stop()

	editor.expectStopped(t)
}

func TestLayoutEditDoesNotOpenALayoutFileWithProblems(t *testing.T) {
	directory := useConfigDirectory(t)
	trustAlready(t, directory, deskNextDoor)
	file := layoutFileIn(directory)
	file.save(t, "desk-next-door stranger\n\ndesk-next-door\n")
	opened := browserThat(t, nil)

	exitCode, stdout, stderr := runLayoutEditThatMustStopByItself(t, "--edit")

	if exitCode != 1 {
		t.Errorf("exit code = %d, want 1", exitCode)
	}
	if want := fmt.Sprintf("The layout file is %s\n\n", file.path); stdout != want {
		t.Errorf("stdout = %q, want only %q", stdout, want)
	}
	for _, want := range []string{
		"the layout file has problems, so it cannot be arranged by dragging:",
		`  line 1: "stranger" is not a known machine`,
		`  line 3: "desk-next-door" is repeated, first seen on line 1`,
		"Fix those lines by hand, or remove the file to start from an empty desk.",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, which lacks %q", stderr, want)
		}
	}
	expectNeverOpened(t, opened)
}

func TestLayoutEditFailsOnADamagedListOfTrustedMachines(t *testing.T) {
	directory := useConfigDirectory(t)
	layoutFileIn(directory).save(t, "desk-next-door\n")
	if err := os.WriteFile(filepath.Join(directory, peersFileName), []byte("{"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	opened := browserThat(t, nil)

	exitCode, _, stderr := runLayoutEditThatMustStopByItself(t, "--edit")

	if exitCode != 1 || !strings.Contains(stderr, "is damaged") {
		t.Errorf("exit code = %d, stderr = %q, want 1 and the damaged list of trusted machines", exitCode, stderr)
	}
	expectNeverOpened(t, opened)
}

func TestLayoutEditWithAnArgumentOrAnUnknownOptionShowsUsageAndOpensNothing(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "an argument after the option", args: []string{"--edit", "desk"}, want: "layout takes no argument besides --edit"},
		{name: "an argument before the option", args: []string{"desk", "--edit"}, want: "layout takes no argument besides --edit"},
		{name: "an option that does not exist", args: []string{"--move"}, want: "flag provided but not defined: -move"},
		{name: "a value that is not yes or no", args: []string{"--edit=desk"}, want: "invalid boolean value"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			directory := useConfigDirectory(t)
			opened := browserThat(t, nil)

			exitCode, _, stderr := runLayoutEditThatMustStopByItself(t, c.args...)

			if exitCode != 2 || !strings.Contains(stderr, c.want) || !strings.Contains(stderr, "Usage:") {
				t.Errorf("exit code = %d, stderr = %q, want 2, %q and the usage", exitCode, stderr, c.want)
			}
			expectNeverOpened(t, opened)
			if _, err := os.Stat(directory); !os.IsNotExist(err) {
				t.Errorf("the config directory was created: %v", err)
			}
		})
	}
}

func TestHelpNamesTheLayoutEditor(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"help"}, nobodyTyping(), &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if want := "lanhop layout --edit   arrange the machines on the desk by dragging them, in a browser\n"; !strings.Contains(stdout.String(), want) {
		t.Errorf("help does not say %q", want)
	}
}

func TestTheBrowserIsOpenedWithTheCommandOfTheSystemWhereThereIsAScreen(t *testing.T) {
	cases := []struct {
		name        string
		system      string
		environment map[string]string
		command     string
		found       bool
	}{
		{name: "macOS", system: "darwin", command: "open", found: true},
		{name: "Linux on X11", system: "linux", environment: map[string]string{"DISPLAY": ":0"}, command: "xdg-open", found: true},
		{name: "Linux on Wayland", system: "linux", environment: map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, command: "xdg-open", found: true},
		{name: "Linux with no graphical session", system: "linux", command: "", found: false},
		{name: "another system with X11", system: "freebsd", environment: map[string]string{"DISPLAY": ":1"}, command: "xdg-open", found: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			command, found := browserCommand(c.system, func(name string) string { return c.environment[name] })

			if command != c.command || found != c.found {
				t.Errorf("browserCommand = %q, %v, want %q, %v", command, found, c.command, c.found)
			}
		})
	}
}

func TestLaunchReportsACommandThatFailsOrDoesNotExist(t *testing.T) {
	cases := []struct {
		name    string
		command string
		fails   bool
	}{
		{name: "a command that succeeds", command: "true", fails: false},
		{name: "a command that fails", command: "false", fails: true},
		{name: "a command that is not installed", command: "lanhop-no-such-browser-opener", fails: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := launch(context.Background(), exec.Command(c.command), testPatience)

			if (err != nil) != c.fails {
				t.Errorf("launch(%s) = %v, want an error: %v", c.command, err, c.fails)
			}
		})
	}
}

func TestLaunchDoesNotWaitForACommandThatKeepsRunning(t *testing.T) {
	cases := []struct {
		name     string
		patience time.Duration
		waiting  func() (context.Context, context.CancelFunc)
	}{
		{
			name:     "longer than its patience",
			patience: 10 * time.Millisecond,
			waiting:  func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
		},
		{
			name:     "after the editor stopped",
			patience: time.Hour,
			waiting: func() (context.Context, context.CancelFunc) {
				done, stop := context.WithCancel(context.Background())
				stop()
				return done, stop
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			done, stop := c.waiting()
			defer stop()
			stillOpen := exec.Command("sleep", "60")
			t.Cleanup(func() {
				if stillOpen.Process != nil {
					stillOpen.Process.Kill()
				}
			})
			returned := make(chan error, 1)

			go func() { returned <- launch(done, stillOpen, c.patience) }()

			select {
			case err := <-returned:
				if err != nil {
					t.Errorf("launch = %v, want no error for a command that started", err)
				}
			case <-time.After(testPatience):
				t.Fatal("launch is still waiting for the command to end")
			}
		})
	}
}
