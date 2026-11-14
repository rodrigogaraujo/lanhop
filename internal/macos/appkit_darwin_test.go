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
	home := Point{X: math.Round(displays[0].X + displays[0].Width/2), Y: math.Round(displays[0].Y + displays[0].Height/2)}
	window, err := openDropWindow(0, []PromisedFile{{Name: "report.pdf"}}, home)
	if err != nil {
		fmt.Println(err)
		return 1
	}
	state := window.state()
	for deadline := time.Now().Add(5 * time.Second); state != windowReady && time.Now().Before(deadline); state = window.state() {
		time.Sleep(20 * time.Millisecond)
	}
	taken, _ := window.takeTheMouseAt(home)
	err = window.close()
	fmt.Printf("window at %+v: state %d, takes the mouse there %v, close %v\n", home, state, taken, err)
	if state != windowReady || !taken || err != nil {
		return 1
	}
	return quitThroughTheShutdownOfLanhop()
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
