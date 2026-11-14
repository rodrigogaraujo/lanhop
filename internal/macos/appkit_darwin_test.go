//go:build darwin && cgo

package macos

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	appKitWindowCheck       = "LANHOP_TEST_APPKIT_WINDOW"
	appKitWindowCheckPassed = 7
	appKitWithoutGUISession = 3
)

func init() {
	if os.Getenv(appKitWindowCheck) == "1" {
		runtime.LockOSThread()
	}
}

func TestMain(m *testing.M) {
	if os.Getenv(appKitWindowCheck) == "1" {
		ServeMainThread(openTheDropWindowThenQuit)
	}
	os.Exit(m.Run())
}

func openTheDropWindowThenQuit() int {
	if err := startAppKit(); errors.Is(err, ErrNoGUISession) {
		fmt.Println(err)
		return appKitWithoutGUISession
	} else if err != nil {
		fmt.Println("AppKit:", err)
		return 1
	}
	displays := Quartz{}.Displays()
	if len(displays) == 0 {
		fmt.Println("no display")
		return 1
	}
	main := displays[0]
	home := Point{X: math.Round(main.X + main.Width/3), Y: math.Round(main.Y + main.Height/4)}
	elsewhere := Point{X: math.Round(main.X + main.Width*2/3), Y: math.Round(main.Y + main.Height*3/5)}
	shielded := Point{X: math.Round(main.X + main.Width/5), Y: math.Round(main.Y + main.Height*4/5)}
	if code := shieldWithoutTakingTheFocus(shielded); code != 0 {
		return code
	}
	window, err := openDropWindow(0, []PromisedFile{{Name: "report.pdf"}}, home)
	if err != nil {
		fmt.Println(err)
		return 1
	}
	state := window.state()
	for deadline := time.Now().Add(5 * time.Second); state != windowReady && time.Now().Before(deadline); state = window.state() {
		time.Sleep(20 * time.Millisecond)
	}
	taken, tries := false, 0
	for deadline := time.Now().Add(2 * time.Second); !taken && err == nil && time.Now().Before(deadline); tries++ {
		if tries > 0 {
			time.Sleep(20 * time.Millisecond)
		}
		taken, err = window.takeTheMouseAt(elsewhere)
	}
	if err == nil {
		err = window.close()
	}
	fmt.Printf("window opened at %+v: state %d; moved to %+v: takes the mouse there %v after %d tries; close %v\n", home, state, elsewhere, taken, tries, err)
	if state != windowReady || !taken || err != nil {
		return 1
	}
	return quitThroughTheShutdownOfLanhop()
}

func shieldWithoutTakingTheFocus(at Point) int {
	shield, err := shieldUnder(context.Background(), func() Point { return at })
	if err != nil {
		fmt.Println("shield:", err)
		return 1
	}
	active := appIsActive()
	err = shield.Close()
	fmt.Printf("shield at %+v took the mouse; lanhop active %v; close %v\n", at, active, err)
	if active || err != nil {
		return 1
	}
	return 0
}

func quitThroughTheShutdownOfLanhop() int {
	terminated := make(chan os.Signal, 1)
	signal.Notify(terminated, syscall.SIGTERM)
	askAppKitToQuit()
	select {
	case <-terminated:
		fmt.Println("asking AppKit to quit sent SIGTERM to lanhop")
		return appKitWindowCheckPassed
	case <-time.After(10 * time.Second):
		fmt.Println("asking AppKit to quit sent no SIGTERM within 10 s")
		return 1
	}
}

func TestTheDropWindowAndAQuitOfTheAppWorkOnTheAppKitRunLoopOfTheMainThread(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^$")
	child.Env = append(os.Environ(), appKitWindowCheck+"=1")
	output, err := child.CombinedOutput()
	if child.ProcessState == nil {
		t.Fatalf("the AppKit check did not run: %v", err)
	}
	switch code := child.ProcessState.ExitCode(); {
	case code == appKitWindowCheckPassed && strings.Contains(string(output), "did not exit within"):
		t.Fatalf("the exit code reached the process, but not through the main thread:\n%s", output)
	case code == appKitWindowCheckPassed:
		t.Logf("%s", output)
	case code == appKitWithoutGUISession && os.Getenv("CI") == "":
		t.Skipf("no GUI session here: %s", output)
	default:
		t.Fatalf("the AppKit check ended with exit code %d (%v), want %d:\n%s", code, err, appKitWindowCheckPassed, output)
	}
}
