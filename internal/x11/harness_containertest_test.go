//go:build linux && containertest

package x11

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/event"
)

type containerTestLog struct {
	mutex   sync.Mutex
	written strings.Builder
}

func (l *containerTestLog) Write(p []byte) (int, error) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	return l.written.Write(p)
}

func (l *containerTestLog) String() string {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	return l.written.String()
}

func (l *containerTestLog) waitFor(t *testing.T, line string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(l.String(), line) {
		if time.Now().After(deadline) {
			t.Fatalf("the log never said %q:\n%s", line, l.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type containerTestDisplay struct {
	replay *XTestReplay
	probe  *DisplayProbe
	width  int
	height int
}

func startContainerTestDisplay(t *testing.T) *containerTestDisplay {
	t.Helper()
	t.Setenv("DISPLAY", startXvfb(t))
	t.Setenv("XAUTHORITY", filepath.Join(t.TempDir(), "no-cookies"))
	replay, err := OpenXTestReplay()
	if err != nil {
		t.Fatalf("OpenXTestReplay: %v", err)
	}
	t.Cleanup(func() { replay.Close() })
	probe, err := OpenDisplayProbe()
	if err != nil {
		t.Fatalf("OpenDisplayProbe: %v", err)
	}
	t.Cleanup(func() { probe.Close() })
	return &containerTestDisplay{replay: replay, probe: probe, width: replay.width, height: replay.height}
}

func (d *containerTestDisplay) emit(t *testing.T, events ...event.Event) {
	t.Helper()
	if err := d.replay.Emit(events...); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if _, _, _, err := d.replay.h.pointer(); err != nil {
		t.Fatalf("sync the replay with the X server: %v", err)
	}
}

func (d *containerTestDisplay) moveTo(t *testing.T, x, y int) {
	t.Helper()
	toX := int32(math.Round(float64(x) * float64(event.AbsoluteMax) / float64(d.width-1)))
	toY := int32(math.Round(float64(y) * float64(event.AbsoluteMax) / float64(d.height-1)))
	d.emit(t, event.Absolute(event.AbsX, toX), event.Absolute(event.AbsY, toY), event.Sync())
}

func (d *containerTestDisplay) moveAlong(t *testing.T, path ...[2]int) {
	t.Helper()
	for _, at := range path {
		d.moveTo(t, at[0], at[1])
		time.Sleep(10 * time.Millisecond)
	}
}

func (d *containerTestDisplay) expectPointer(t *testing.T, x, y int) {
	t.Helper()
	gotX, gotY, err := d.probe.Pointer()
	if err != nil {
		t.Fatalf("Pointer: %v", err)
	}
	if gotX != x || gotY != y {
		t.Errorf("the pointer is at %d,%d, want %d,%d", gotX, gotY, x, y)
	}
}

func (d *containerTestDisplay) buttons(t *testing.T) uint16 {
	t.Helper()
	_, _, buttons, err := d.probe.h.pointer()
	if err != nil {
		t.Fatalf("QueryPointer: %v", err)
	}
	return buttons & (0x100 | 0x200 | 0x400)
}

func (d *containerTestDisplay) keyDown(t *testing.T, keycode int) bool {
	t.Helper()
	const opQueryKeymapRequest = 44
	reply, err := d.probe.h.call(newRequest(opQueryKeymapRequest, 0).done())
	if err != nil {
		t.Fatalf("QueryKeymap: %v", err)
	}
	return reply[8+keycode/8]&(1<<(keycode%8)) != 0
}

func TestTheContainerTestReplayMovesThePointerOnlyAtTheEndOfAReport(t *testing.T) {
	d := startContainerTestDisplay(t)

	d.emit(t, event.Absolute(event.AbsX, 0), event.Absolute(event.AbsY, 0), event.Sync())
	d.expectPointer(t, 0, 0)
	d.emit(t, event.Absolute(event.AbsX, event.AbsoluteMax), event.Absolute(event.AbsY, event.AbsoluteMax), event.Sync())
	d.expectPointer(t, d.width-1, d.height-1)
	d.moveTo(t, 300, 200)
	d.expectPointer(t, 300, 200)

	d.emit(t, event.Relative(event.RelX, 7), event.Relative(event.RelY, -4), event.Relative(event.RelX, 3))
	d.expectPointer(t, 300, 200)
	d.emit(t, event.Sync())
	d.expectPointer(t, 310, 196)

	d.emit(t, event.Absolute(event.AbsX, event.AbsoluteMax/2), event.Sync())
	d.expectPointer(t, xtestScaled(event.AbsoluteMax/2, d.width), 196)
}

func TestTheContainerTestReplayStopsAMotionBeyondTheX11RangeAtTheEdge(t *testing.T) {
	d := startContainerTestDisplay(t)
	d.moveTo(t, 300, 200)
	d.emit(t, event.Relative(event.RelX, 30000), event.Relative(event.RelX, 20000), event.Relative(event.RelY, -50000), event.Sync())
	d.expectPointer(t, d.width-1, 0)
}

func TestTheContainerTestReplayPressesButtonsAsXDoesAndKeysEightCodesHigher(t *testing.T) {
	d := startContainerTestDisplay(t)
	for _, button := range []struct {
		code uint16
		mask uint16
	}{{event.ButtonLeft, 0x100}, {event.ButtonMiddle, 0x200}, {event.ButtonRight, 0x400}} {
		d.emit(t, event.Key(button.code, event.KeyPressed), event.Sync())
		if got := d.buttons(t); got != button.mask {
			t.Errorf("after pressing %#x, buttons held = %#x, want %#x", button.code, got, button.mask)
		}
		d.emit(t, event.Key(button.code, event.KeyReleased), event.Sync())
		if got := d.buttons(t); got != 0 {
			t.Errorf("after releasing %#x, buttons held = %#x, want none", button.code, got)
		}
	}

	keycodeOfA := int(event.KeyA) + 8
	d.emit(t, event.Key(event.KeyA, event.KeyPressed), event.Sync())
	if !d.keyDown(t, keycodeOfA) {
		t.Errorf("keycode %d is not down after pressing KEY_A", keycodeOfA)
	}
	d.emit(t, event.Key(event.KeyA, event.KeyReleased), event.Sync())
	if d.keyDown(t, keycodeOfA) {
		t.Errorf("keycode %d is still down after releasing KEY_A", keycodeOfA)
	}
}

type containerTestDrag struct {
	*containerTestDisplay
	file      string
	contents  []byte
	source    *XdndTestSource
	sourceLog *containerTestLog
	target    *XdndTestTarget
	targetLog *containerTestLog
	took      chan []string
}

func startContainerTestDrag(t *testing.T, answerMove bool) *containerTestDrag {
	t.Helper()
	drag := &containerTestDrag{
		containerTestDisplay: startContainerTestDisplay(t),
		file:                 filepath.Join(t.TempDir(), "report 2026.bin"),
		contents:             []byte("the bytes of a report\n"),
		sourceLog:            &containerTestLog{},
		targetLog:            &containerTestLog{},
		took:                 make(chan []string, 1),
	}
	if err := os.WriteFile(drag.file, drag.contents, 0o600); err != nil {
		t.Fatal(err)
	}
	var err error
	drag.target, err = StartXdndTestTarget(XdndTestTargetConfig{
		X: 400, Y: 100, Width: 300, Height: 300, AnswerMove: answerMove, Log: drag.targetLog,
		Take: func(paths []string) error {
			drag.took <- paths
			return nil
		},
	})
	if err != nil {
		t.Fatalf("StartXdndTestTarget: %v", err)
	}
	t.Cleanup(func() { drag.target.Close() })
	drag.source, err = StartXdndTestSource(XdndTestSourceConfig{File: drag.file, X: 20, Y: 20, Width: 200, Height: 200, Log: drag.sourceLog})
	if err != nil {
		t.Fatalf("StartXdndTestSource: %v", err)
	}
	t.Cleanup(func() { drag.source.Close() })
	return drag
}

func (drag *containerTestDrag) pressInTheSource(t *testing.T) {
	t.Helper()
	drag.moveTo(t, 100, 100)
	drag.emit(t, event.Key(event.ButtonLeft, event.KeyPressed), event.Sync())
	drag.sourceLog.waitFor(t, fmt.Sprintf("drag-started window=%d", drag.source.Window()))
	owner, err := drag.probe.SelectionOwner("XdndSelection")
	if err != nil || owner != drag.source.Window() {
		t.Fatalf("XdndSelection is owned by %d (%v), want the source window %d", owner, err, drag.source.Window())
	}
}

func (drag *containerTestDrag) release(t *testing.T) {
	t.Helper()
	drag.emit(t, event.Key(event.ButtonLeft, event.KeyReleased), event.Sync())
}

func (drag *containerTestDrag) expectFile(t *testing.T) {
	t.Helper()
	contents, err := os.ReadFile(drag.file)
	if err != nil || !bytes.Equal(contents, drag.contents) {
		t.Errorf("the dragged file is %q (%v), want it untouched", contents, err)
	}
}

func (drag *containerTestDrag) expectSelectionLetGo(t *testing.T) {
	t.Helper()
	drag.sourceLog.waitFor(t, "drag-ended")
	if owner, _ := drag.probe.SelectionOwner("XdndSelection"); owner == drag.source.Window() {
		t.Error("the source still owns XdndSelection after its drag ended")
	}
}

func (drag *containerTestDrag) dropOnTheTarget(t *testing.T, action string) {
	t.Helper()
	drag.pressInTheSource(t)
	drag.moveAlong(t, [2]int{200, 150}, [2]int{350, 200}, [2]int{450, 220}, [2]int{550, 250})
	drag.sourceLog.waitFor(t, fmt.Sprintf("status window=%d accept=true action=%s", drag.target.Window(), action))
	drag.release(t)
	select {
	case paths := <-drag.took:
		if len(paths) != 1 || paths[0] != drag.file {
			t.Errorf("the target took %q, want only %q", paths, drag.file)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("the target took nothing\nsource:\n%s\ntarget:\n%s", drag.sourceLog, drag.targetLog)
	}
	drag.sourceLog.waitFor(t, fmt.Sprintf("finished window=%d accepted=true action=%s", drag.target.Window(), action))
	drag.expectSelectionLetGo(t)
}

func TestTheContainerTestSourceKeepsItsFileWhenTheDropFinishesWithCopy(t *testing.T) {
	drag := startContainerTestDrag(t, false)
	drag.dropOnTheTarget(t, "XdndActionCopy")
	drag.sourceLog.waitFor(t, "kept path="+drag.file)
	drag.expectFile(t)
	drag.targetLog.waitFor(t, "uri-list=true")
	drag.sourceLog.waitFor(t, "asked target=text/uri-list given=true")
	drag.sourceLog.waitFor(t, fmt.Sprintf("enter window=%d version=5 input-only=false override-redirect=false x=400 y=100 width=300 height=300", drag.target.Window()))
	if strings.Contains(drag.sourceLog.String(), "request-failed") {
		t.Errorf("a request of the source failed:\n%s", drag.sourceLog)
	}
}

func TestTheContainerTestSourceDroppedOnLanhopsEdgeWindowKeepsItsFile(t *testing.T) {
	drag := startContainerTestDrag(t, false)
	d, err := parseDisplay(os.Getenv("DISPLAY"))
	if err != nil {
		t.Fatal(err)
	}
	lanhop, err := openDrags(d, nil)
	if err != nil {
		t.Fatalf("openDrags: %v", err)
	}
	t.Cleanup(func() { lanhop.Close() })
	strip := Rect{X: drag.width - 4, Y: 0, Width: 4, Height: drag.height}
	catch, err := lanhop.CatchAtEdge(strip)
	if err != nil {
		t.Fatalf("CatchAtEdge: %v", err)
	}
	t.Cleanup(func() { catch.Close() })

	drag.pressInTheSource(t)
	drag.moveAlong(t, [2]int{800, 120}, [2]int{drag.width - 10, 120}, [2]int{drag.width - 2, 120}, [2]int{drag.width - 3, 120})
	select {
	case <-catch.Accepted():
	case <-time.After(5 * time.Second):
		t.Fatalf("the edge window never accepted the drag:\n%s", drag.sourceLog)
	}
	drag.release(t)
	select {
	case dropped := <-catch.Dropped():
		if dropped.Err != nil || len(dropped.Paths) != 1 || dropped.Paths[0] != drag.file {
			t.Errorf("the edge window got %q (%v), want only %q", dropped.Paths, dropped.Err, drag.file)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("nothing was dropped on the edge window:\n%s", drag.sourceLog)
	}
	described := fmt.Sprintf("version=5 input-only=true override-redirect=true x=%d y=0 width=4 height=%d", strip.X, strip.Height)
	drag.sourceLog.waitFor(t, described)
	drag.sourceLog.waitFor(t, "accepted=true action=XdndActionCopy")
	drag.sourceLog.waitFor(t, "kept path="+drag.file)
	drag.expectSelectionLetGo(t)
	drag.expectFile(t)
}

func TestTheContainerTestSourceDeletesItsFileWhenTheDropFinishesWithMove(t *testing.T) {
	drag := startContainerTestDrag(t, true)
	drag.dropOnTheTarget(t, "XdndActionMove")
	drag.sourceLog.waitFor(t, "deleted path="+drag.file)
	if _, err := os.Stat(drag.file); !os.IsNotExist(err) {
		t.Errorf("the file is still there after a move: %v", err)
	}
}

func TestTheContainerTestSourceReleasedOverNoTargetDropsNothing(t *testing.T) {
	drag := startContainerTestDrag(t, false)
	drag.pressInTheSource(t)
	drag.moveAlong(t, [2]int{250, 500}, [2]int{300, 600})
	drag.release(t)
	drag.sourceLog.waitFor(t, "released-without-drop reason=no-target")
	drag.expectSelectionLetGo(t)
	drag.expectFile(t)
	if strings.Contains(drag.targetLog.String(), "enter") {
		t.Errorf("the target saw the drag:\n%s", drag.targetLog)
	}
}

func TestTheContainerTestSourceOffersTheAddressAndRefusesThePortalTypes(t *testing.T) {
	drag := startContainerTestDrag(t, false)
	drag.pressInTheSource(t)
	for _, ask := range []struct {
		target string
		want   string
	}{
		{"application/vnd.portal.filetransfer", ""},
		{"application/vnd.portal.files", ""},
		{"text/uri-list", "file://" + strings.ReplaceAll(drag.file, " ", "%20") + "\r\n"},
	} {
		if got := askTheContainerTestSource(t, ask.target); got != ask.want {
			t.Errorf("asked for %s, got %q, want %q", ask.target, got, ask.want)
		}
	}
	drag.release(t)
	drag.expectSelectionLetGo(t)
}

func askTheContainerTestSource(t *testing.T, target string) string {
	t.Helper()
	events := make(chan []byte, 64)
	d, err := parseDisplay(os.Getenv("DISPLAY"))
	if err != nil {
		t.Fatal(err)
	}
	asker, err := dialHarness(d, nil, func(event []byte) {
		select {
		case events <- event:
		default:
		}
	}, "XdndSelection", target, "LANHOP_TEST_ASKED")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer asker.close()
	window, err := asker.createWindow(0, 0, 1, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	asker.send(newRequest(opConvertSelection, 0).u32(window).u32(asker.atoms["XdndSelection"]).u32(asker.atoms[target]).u32(asker.atoms["LANHOP_TEST_ASKED"]).u32(currentTime).done())
	deadline := time.After(3 * time.Second)
	for {
		select {
		case event := <-events:
			if event[0]&^sentEventFlag != eventSelectionNotify {
				continue
			}
			if binary.LittleEndian.Uint32(event[20:24]) == atomNone {
				return ""
			}
			_, data, err := asker.takeProperty(window, asker.atoms["LANHOP_TEST_ASKED"])
			if err != nil {
				t.Fatalf("GetProperty: %v", err)
			}
			return string(data)
		case <-deadline:
			t.Fatalf("the source did not answer a request for %s", target)
			return ""
		}
	}
}
