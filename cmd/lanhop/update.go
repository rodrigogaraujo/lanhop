package main

import (
	"context"
	"crypto/ed25519"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/update"
)

const (
	downloadPatience = 5 * time.Minute
	updatesFileName  = "updates"
	appSuffix        = ".app"
	appProgram       = "Contents/MacOS/lanhop"
)

var (
	answerPatience     = 10 * time.Second
	projectReleaseKeys []ed25519.PublicKey
	releasesAddress    = update.ProjectReleases
	checkNewApp        func(app string) error
	firstProgramString = regexp.MustCompile(`(?s)<key>ProgramArguments</key>\s*<array>\s*<string>([^<]+)</string>`)
)

var errNoReleaseKey = errors.New("this build of lanhop carries no release key, so it cannot check a download. Update it with the installer line")

func updateThisMachine(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("update", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	asked := flags.String("version", "", "")
	older := flags.Bool("older", false, "")
	if err := flags.Parse(args); errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(stdout, usage)
		return nil
	} else if err != nil {
		return usageError(err.Error())
	}
	if flags.NArg() > 0 {
		return usageError("update takes no argument besides its options")
	}
	if *older && *asked == "" {
		return usageError("--older goes with --version, which names the older release")
	}
	if len(projectReleaseKeys) == 0 {
		return errNoReleaseKey
	}
	directory, err := configDirectory()
	if err != nil {
		return err
	}
	unlock, err := waitForTheUpdateLock(directory)
	if err != nil {
		return err
	}
	defer unlock()

	target, err := installedTarget()
	if err != nil {
		return err
	}
	if err := update.CleanUp(target); err != nil {
		fmt.Fprintf(stdout, "A folder left by an earlier update could not be removed: %v\n", err)
	}
	ctx, stop := context.WithTimeout(context.Background(), downloadPatience)
	defer stop()
	source := update.Source{Releases: releasesAddress, Client: patientClient(), Keys: projectReleaseKeys, CheckApp: checkNewApp}

	want, err := wantedRelease(ctx, source, *asked)
	if err != nil {
		return err
	}
	comparison := want.Compare(target.Running)
	switch {
	case comparison == 0:
		fmt.Fprintf(stdout, "lanhop %s is the release asked for already. Nothing changed.\n", target.Running)
		return nil
	case comparison < 0 && !*older:
		return fmt.Errorf("%s is older than lanhop %s here. To move back to it, run: lanhop update --version %s --older", want, target.Running, want)
	}

	staged, err := source.Prepare(ctx, target, want, *older)
	if errors.Is(err, update.ErrNeedsInstaller) {
		return fmt.Errorf("%w. Type the installer line instead:\n\n  %s", err, installerLine(want.String(), ""))
	}
	if err != nil {
		return err
	}
	if err := update.Install(staged, target); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "lanhop %s → %s\n", target.Running, want)
	restartTheService(stdout)
	if comparison < 0 {
		if err := writeUpdatesSwitch(directory, false); err != nil {
			return fmt.Errorf("lanhop moved back to %s, but could not stop following the machine with the keyboard, which may move it up again: %w", want, err)
		}
		fmt.Fprintln(stdout, "This machine no longer follows the version of the machine with the keyboard, so that it is not moved back up. lanhop updates on lets it follow again.")
	}
	return nil
}

func wantedRelease(ctx context.Context, source update.Source, asked string) (update.Version, error) {
	if asked != "" {
		want, err := update.ParseVersion(asked)
		if err != nil {
			return update.Version{}, usageError(fmt.Sprintf("--version %q is not a release, which looks like v1.2.3", asked))
		}
		return want, nil
	}
	return source.Latest(ctx)
}

func patientClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: answerPatience}).DialContext
	transport.TLSHandshakeTimeout = answerPatience
	transport.ResponseHeaderTimeout = answerPatience
	return &http.Client{Transport: transport}
}

func installedTarget() (update.Target, error) {
	program, err := installedProgram()
	if err != nil {
		return update.Target{}, err
	}
	target := update.Target{Program: program, System: runtime.GOOS, Arch: runtime.GOARCH}
	runnable := program
	if runtime.GOOS == "darwin" {
		app := filepath.Dir(filepath.Dir(filepath.Dir(program)))
		if filepath.Base(app) != "lanhop"+appSuffix || filepath.Join(app, appProgram) != program {
			return update.Target{}, fmt.Errorf("lanhop runs from %s, which is not inside a lanhop.app, so it is not replaced. Update it with the installer line:\n\n  %s", program, installerLine("", ""))
		}
		target.Program = app
	} else if filepath.Base(program) != "lanhop" {
		return update.Target{}, fmt.Errorf("lanhop runs from %s, which is not named lanhop, so it is not replaced. Update it with the installer line:\n\n  %s", program, installerLine("", ""))
	}
	if err := writableFolder(filepath.Dir(target.Program)); err != nil {
		return update.Target{}, fmt.Errorf("lanhop sits in %s, which this user cannot write, so it is not replaced. Update it with the installer line:\n\n  %s", filepath.Dir(target.Program), installerLine("", ""))
	}
	running, err := versionOfInstalled(runnable)
	if err != nil {
		return update.Target{}, err
	}
	target.Running = running
	return target, nil
}

func installedProgram() (string, error) {
	if program, found := programOfTheService(); found {
		resolved, err := filepath.EvalSymlinks(program)
		if err != nil {
			return "", fmt.Errorf("the lanhop service starts %s, which is not there any more. Type the installer line again:\n\n  %s", program, installerLine("", ""))
		}
		return resolved, nil
	}
	running, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(running)
}

func programOfTheService() (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	if agent, err := os.ReadFile(filepath.Join(home, "Library", "LaunchAgents", agentLabel+".plist")); err == nil {
		if found := firstProgramString.FindSubmatch(agent); found != nil {
			return string(found[1]), true
		}
	}
	units := os.Getenv("XDG_CONFIG_HOME")
	if units == "" {
		units = filepath.Join(home, ".config")
	}
	unit, err := os.ReadFile(filepath.Join(units, "systemd", "user", unitName))
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(unit), "\n") {
		command, found := strings.CutPrefix(line, "ExecStart=")
		if !found {
			continue
		}
		if quoted, _, closed := strings.Cut(strings.TrimPrefix(command, `"`), `"`); strings.HasPrefix(command, `"`) && closed {
			return quoted, true
		}
		if fields := strings.Fields(command); len(fields) > 0 {
			return fields[0], true
		}
	}
	return "", false
}

func versionOfInstalled(program string) (update.Version, error) {
	patient, stop := context.WithTimeout(context.Background(), answerPatience)
	defer stop()
	asked := exec.CommandContext(patient, program, "version")
	asked.Env = slices.DeleteFunc(os.Environ(), func(variable string) bool { return strings.HasPrefix(variable, "LANHOP_") })
	said, err := asked.Output()
	if err != nil {
		return update.Version{}, fmt.Errorf("the installed lanhop, %s, did not run: %w", program, err)
	}
	text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(said)), "lanhop "))
	running, err := update.ParseVersion(text)
	if err != nil {
		return update.Version{}, fmt.Errorf("the installed lanhop is %s, a build of the source, so it is not replaced", text)
	}
	return running, nil
}

func writableFolder(folder string) error {
	probe, err := os.CreateTemp(folder, ".lanhop-write-check-*")
	if err != nil {
		return err
	}
	probe.Close()
	return os.Remove(probe.Name())
}

func writeUpdatesSwitch(directory string, on bool) error {
	path := filepath.Join(directory, updatesFileName)
	if on {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("off\n"), 0o600)
}

func restartTheService(stdout io.Writer) {
	var restart *exec.Cmd
	switch {
	case runtime.GOOS == "darwin" && serviceDefinitionExists(true):
		restart = exec.Command("launchctl", "kickstart", "-k", "gui/"+strconv.Itoa(os.Getuid())+"/"+agentLabel)
	case runtime.GOOS != "darwin" && serviceDefinitionExists(false) && exec.Command("systemctl", "--user", "is-active", "--quiet", unitName).Run() == nil:
		restart = exec.Command("systemctl", "--user", "restart", unitName)
	}
	if restart == nil {
		fmt.Fprintln(stdout, "Start lanhop again to use the new version.")
		return
	}
	output, err := restart.CombinedOutput()
	if err != nil {
		fmt.Fprintf(stdout, "The lanhop service did not restart (%v): %s\nStart lanhop again to use the new version.\n", err, strings.TrimSpace(string(output)))
		return
	}
	fmt.Fprintln(stdout, "The lanhop service restarted on the new version. lanhop status says what it does.")
}

func serviceDefinitionExists(launchAgent bool) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	if launchAgent {
		_, err := os.Stat(filepath.Join(home, "Library", "LaunchAgents", agentLabel+".plist"))
		return err == nil
	}
	units := os.Getenv("XDG_CONFIG_HOME")
	if units == "" {
		units = filepath.Join(home, ".config")
	}
	_, err = os.Stat(filepath.Join(units, "systemd", "user", unitName))
	return err == nil
}
