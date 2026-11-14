//go:build linux

package x11

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

type dropOutcome struct {
	report DropReport
	err    error
}

func scratchFileURI(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "report for the desk.txt")
	if err := os.WriteFile(path, []byte("dragged"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return FileURI(path)
}

func startDrop(t *testing.T, source *Drags, options DropOptions) <-chan dropOutcome {
	t.Helper()
	grabbed := make(chan struct{})
	options.Grabbed = func() { close(grabbed) }
	lines := make(chan string, 256)
	options.Say = func(line string) {
		select {
		case lines <- line:
		default:
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	outcome := make(chan dropOutcome, 1)
	go func() {
		defer cancel()
		defer close(lines)
		report, err := source.Drop(ctx, options)
		outcome <- dropOutcome{report, err}
	}()
	t.Cleanup(func() {
		for line := range lines {
			t.Log(line)
		}
	})
	select {
	case <-grabbed:
	case got := <-outcome:
		t.Fatalf("Drop ended before it grabbed the pointer: %+v, %v", got.report, got.err)
	case <-time.After(5 * time.Second):
		t.Fatal("Drop did not grab the pointer")
	}
	return outcome
}

func waitForDrop(t *testing.T, outcome <-chan dropOutcome) DropReport {
	t.Helper()
	select {
	case got := <-outcome:
		if got.err != nil {
			t.Fatalf("Drop: %v (report %+v)", got.err, got.report)
		}
		return got.report
	case <-time.After(20 * time.Second):
		t.Fatal("Drop did not end")
		return DropReport{}
	}
}

func dragAlong(t *testing.T, input *fakeInput, path ...[2]int) {
	t.Helper()
	for _, at := range path {
		if err := input.moveTo(at[0], at[1]); err != nil {
			t.Fatalf("move to %v: %v", at, err)
		}
		time.Sleep(15 * time.Millisecond)
	}
}

func intoTheTarget() [][2]int {
	return [][2]int{{60, 60}, {110, 110}, {150, 150}, {180, 170}, {200, 200}}
}

func TestAPureGoSourceDropsAFileIntoAnXdndTarget(t *testing.T) {
	display := startXvfb(t)
	target := startXdndTarget(t, display, 100, 100, 200, 200, targetBehaviour{accept: true, finish: true})
	source := openDragsOn(t, display)
	input := startFakeInput(t, display)
	input.moveTo(20, 20)
	uri := scratchFileURI(t)

	outcome := startDrop(t, source, DropOptions{URIs: []string{uri}})
	input.press(1)
	dragAlong(t, input, intoTheTarget()...)
	input.release(1)
	report := waitForDrop(t, outcome)

	if !report.Dropped || !report.Finished || !report.FinishAccepted {
		t.Errorf("report = %+v, want a drop that the target finished and accepted", report)
	}
	if report.Target != target.window || report.Version != xdndVersion {
		t.Errorf("target 0x%x version %d, want 0x%x version %d", report.Target, report.Version, target.window, xdndVersion)
	}
	if report.Action != "XdndActionCopy" || report.FinishAction != "XdndActionCopy" {
		t.Errorf("actions = %q and %q, want XdndActionCopy for both", report.Action, report.FinishAction)
	}
	select {
	case got := <-target.dropped:
		if got != uri+"\r\n" {
			t.Errorf("the target received %q, want %q", got, uri+"\r\n")
		}
	default:
		t.Error("the target received no address")
	}
	version, types := target.entered()
	if version != xdndVersion || !slices.Contains(types, target.atoms["text/uri-list"]) || len(types) != 1 {
		t.Errorf("XdndEnter carried version %d and %d types, want version 5 with text/uri-list only", version, len(types))
	}
	seen := target.messages()
	if len(seen) < 3 || seen[0] != "XdndEnter" || seen[len(seen)-1] != "XdndDrop" || !slices.Contains(seen, "XdndPosition") || slices.Contains(seen, "XdndLeave") {
		t.Errorf("the target saw %v, want XdndEnter, XdndPosition and XdndDrop last, and no XdndLeave", seen)
	}
	if !slices.ContainsFunc(report.Served, func(line string) bool { return strings.HasPrefix(line, "served text/uri-list") }) {
		t.Errorf("served %v, want text/uri-list", report.Served)
	}
	if report.Grab != GrabSuccess || report.Positions < 1 || report.Statuses < 1 {
		t.Errorf("grab %s, %d positions, %d statuses", report.Grab, report.Positions, report.Statuses)
	}
}

func TestADropCanStartAtOnceWhileAButtonIsHeld(t *testing.T) {
	display := startXvfb(t)
	target := startXdndTarget(t, display, 100, 100, 200, 200, targetBehaviour{accept: true, finish: true})
	source := openDragsOn(t, display)
	input := startFakeInput(t, display)
	input.moveTo(20, 20)
	input.press(1)
	uri := scratchFileURI(t)

	outcome := startDrop(t, source, DropOptions{URIs: []string{uri}, StartNow: true})
	dragAlong(t, input, intoTheTarget()...)
	input.release(1)
	report := waitForDrop(t, outcome)

	if !report.Dropped || !report.Finished || report.Target != target.window {
		t.Errorf("report = %+v, want a finished drop on 0x%x", report, target.window)
	}
	select {
	case got := <-target.dropped:
		if got != uri+"\r\n" {
			t.Errorf("the target received %q, want %q", got, uri+"\r\n")
		}
	default:
		t.Error("the target received no address")
	}
}

func TestATargetThatRefusesGetsXdndLeaveAndNoDrop(t *testing.T) {
	display := startXvfb(t)
	target := startXdndTarget(t, display, 100, 100, 200, 200, targetBehaviour{accept: false, finish: true})
	source := openDragsOn(t, display)
	input := startFakeInput(t, display)
	input.moveTo(20, 20)

	outcome := startDrop(t, source, DropOptions{URIs: []string{scratchFileURI(t)}})
	input.press(1)
	dragAlong(t, input, intoTheTarget()...)
	input.release(1)
	report := waitForDrop(t, outcome)

	if report.Dropped || report.Accepted || report.Outcome != "the target refused the drop" {
		t.Errorf("report = %+v, want a refusal without a drop", report)
	}
	seen := target.messagesOnceItSaw("XdndLeave")
	if slices.Contains(seen, "XdndDrop") || len(seen) == 0 || seen[len(seen)-1] != "XdndLeave" {
		t.Errorf("the target saw %v, want XdndLeave last and no XdndDrop", seen)
	}
}

func TestATargetThatNeverFinishesIsGivenUpAfterTheTimeLimit(t *testing.T) {
	display := startXvfb(t)
	startXdndTarget(t, display, 100, 100, 200, 200, targetBehaviour{accept: true, finish: false})
	source := openDragsOn(t, display)
	input := startFakeInput(t, display)
	input.moveTo(20, 20)
	limit := 300 * time.Millisecond

	outcome := startDrop(t, source, DropOptions{URIs: []string{scratchFileURI(t)}, FinishWait: limit})
	input.press(1)
	dragAlong(t, input, intoTheTarget()...)
	released := time.Now()
	input.release(1)
	report := waitForDrop(t, outcome)
	waited := time.Since(released)

	if !report.Dropped || report.Finished {
		t.Errorf("report = %+v, want a drop that never finished", report)
	}
	if waited < limit || waited > limit+2*time.Second {
		t.Errorf("the source gave up %v after the release, want about %v", waited, limit)
	}
}

func TestWatchSeesANewDragOwnerAndReadsItsFileList(t *testing.T) {
	display := startXvfb(t)
	watcher := openDragsOn(t, display)
	if err := watcher.Watch(); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	source := openDragsOn(t, display)
	uris := []string{"file:///home/someone/report.pdf", FileURI("/home/someone/notes one.txt")}
	when, err := source.serverTime()
	if err != nil {
		t.Fatalf("serverTime: %v", err)
	}

	if err := source.offerDrag(URIList(uris), when); err != nil {
		t.Fatalf("offerDrag: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	change, err := watcher.WaitForDragStart(ctx)
	if err != nil {
		t.Fatalf("WaitForDragStart: %v", err)
	}
	if change.Owner != source.Window() || change.Reason != NewOwner || change.SelectionTime != when {
		t.Errorf("change = %+v, want a new owner 0x%x since %d", change, source.Window(), when)
	}
	offer := watcher.ReadOffer()
	if offer.TargetsErr != nil || !slices.Contains(offer.Targets, "text/uri-list") || !slices.Contains(offer.Targets, "TARGETS") {
		t.Errorf("targets = %v, %v", offer.Targets, offer.TargetsErr)
	}
	want := "file:///home/someone/report.pdf\r\nfile:///home/someone/notes%20one.txt\r\n"
	if offer.URIListErr != nil || offer.URIList != want || !offer.AllFiles() {
		t.Errorf("uri list = %q, %v, all files %v; want %q", offer.URIList, offer.URIListErr, offer.AllFiles(), want)
	}

	source.Close()

	select {
	case gone := <-watcher.OwnerChanges():
		if gone.Reason == NewOwner {
			t.Errorf("after the source closed: %+v, want the owner gone", gone)
		}
	case <-time.After(3 * time.Second):
		t.Error("the watcher saw nothing when the source closed")
	}
	if owner, err := watcher.Owner(); err != nil || owner != atomNone {
		t.Errorf("Owner = 0x%x, %v, want none", owner, err)
	}
}

func TestAWebAddressInTheListIsNotAFileDrag(t *testing.T) {
	if (Offer{URIList: "file:///a\r\nhttps://example.org/\r\n"}).AllFiles() {
		t.Error("a list with a web address counted as files")
	}
	if (Offer{URIList: "# only a comment\r\n"}).AllFiles() {
		t.Error("a list with no address counted as files")
	}
	if !(Offer{URIList: "FILE:///a\r\n"}).AllFiles() {
		t.Error("a file address in capitals did not count")
	}
}

func TestEndingADragWaitsForItToStartThenReleasesTheButton(t *testing.T) {
	display := startXvfb(t)
	source := openDragsOn(t, display)
	canceller := openDragsOn(t, display)
	if err := canceller.Watch(); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	input := startFakeInput(t, display)
	input.moveTo(20, 20)
	wait := 300 * time.Millisecond

	outcome := startDrop(t, source, DropOptions{URIs: []string{scratchFileURI(t)}})
	ended := make(chan time.Time, 1)
	type cancelOutcome struct {
		change OwnerChange
		err    error
	}
	cancelled := make(chan cancelOutcome, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		change, err := canceller.EndDragAfter(ctx, wait, nil, func() error {
			ended <- time.Now()
			return input.release(1)
		})
		cancelled <- cancelOutcome{change, err}
	}()
	input.press(1)
	report := waitForDrop(t, outcome)

	got := <-cancelled
	if got.err != nil {
		t.Fatalf("EndDragAfter: %v", got.err)
	}
	if got.change.Owner != source.Window() {
		t.Errorf("the drag seen was owned by 0x%x, want the source 0x%x", got.change.Owner, source.Window())
	}
	if waited := (<-ended).Sub(got.change.At); waited < wait {
		t.Errorf("the button was released %v after the drag started, want at least %v", waited, wait)
	}
	if report.Dropped || report.Outcome != "released over no window that takes XDND drops" {
		t.Errorf("report = %+v, want the drag ended by the release without a drop", report)
	}
}

func TestTheCursorMovesFromRelativeInputWhileThePointerIsGrabbed(t *testing.T) {
	display := startXvfb(t)
	probe := openDragsOn(t, display)
	input := startFakeInput(t, display)
	input.moveTo(300, 300)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	measure, err := probe.MeasureMoveWhileGrabbed(ctx, input.moveBy, 100, 50, 20*time.Millisecond, true)

	if err != nil {
		t.Fatalf("MeasureMoveWhileGrabbed: %v", err)
	}
	if measure.Grab != GrabSuccess {
		t.Errorf("grab status %s", measure.Grab)
	}
	if dx, dy := measure.BaselineAfter.X-measure.BaselineBefore.X, measure.BaselineAfter.Y-measure.BaselineBefore.Y; dx != 100 || dy != 50 {
		t.Errorf("without a grab the cursor moved by %d,%d, want 100,50", dx, dy)
	}
	if dx, dy := measure.After.X-measure.Before.X, measure.After.Y-measure.Before.Y; dx != -100 || dy != -50 {
		t.Errorf("with the grab the cursor moved by %d,%d, want -100,-50", dx, dy)
	}
	if measure.MotionEvents < 1 {
		t.Error("the grabbing program got no motion event")
	}
}

func TestAPointerHeldByAnotherProgramIsReportedAsAlreadyGrabbed(t *testing.T) {
	display := startXvfb(t)
	holder := openDragsOn(t, display)
	other := openDragsOn(t, display)
	ctx := context.Background()
	if status, _, err := holder.GrabPointer(ctx, time.Second); err != nil || status != GrabSuccess {
		t.Fatalf("first grab: %s, %v", status, err)
	}

	status, tries, err := other.GrabPointer(ctx, 200*time.Millisecond)

	if status != GrabAlreadyGrabbed || err == nil || tries < 2 {
		t.Errorf("second grab = %s after %d tries, %v; want AlreadyGrabbed after retrying", status, tries, err)
	}
}

func TestAFilePathBecomesAnEscapedFileAddress(t *testing.T) {
	if got := FileURI("/home/someone/a b/ação.txt"); got != "file:///home/someone/a%20b/a%C3%A7%C3%A3o.txt" {
		t.Errorf("FileURI = %q", got)
	}
	if got := string(URIList([]string{"file:///a", "file:///b"})); got != "file:///a\r\nfile:///b\r\n" {
		t.Errorf("URIList = %q", got)
	}
}

func TestATargetThatChoosesMoveGetsXdndLeaveAndNoDrop(t *testing.T) {
	display := startXvfb(t)
	target := startXdndTarget(t, display, 100, 100, 200, 200, targetBehaviour{accept: true, finish: true, action: "XdndActionMove"})
	source := openDragsOn(t, display)
	input := startFakeInput(t, display)
	input.moveTo(20, 20)

	outcome := startDrop(t, source, DropOptions{URIs: []string{scratchFileURI(t)}})
	input.press(1)
	dragAlong(t, input, intoTheTarget()...)
	input.release(1)
	report := waitForDrop(t, outcome)

	if report.Dropped || !report.Accepted || report.Action != "XdndActionMove" || !strings.Contains(report.Outcome, "XdndActionMove") {
		t.Errorf("report = %+v, want no drop and the outcome naming XdndActionMove", report)
	}
	seen := target.messagesOnceItSaw("XdndLeave")
	if slices.Contains(seen, "XdndDrop") || len(seen) == 0 || seen[len(seen)-1] != "XdndLeave" {
		t.Errorf("the target saw %v, want XdndLeave last and no XdndDrop", seen)
	}
}

func TestAWheelNotchDoesNotStartADrop(t *testing.T) {
	display := startXvfb(t)
	target := startXdndTarget(t, display, 100, 100, 200, 200, targetBehaviour{accept: true, finish: true})
	source := openDragsOn(t, display)
	input := startFakeInput(t, display)
	input.moveTo(150, 150)

	outcome := startDrop(t, source, DropOptions{URIs: []string{scratchFileURI(t)}})
	input.press(4)
	input.release(4)
	input.press(5)
	input.release(5)

	time.Sleep(200 * time.Millisecond)
	select {
	case got := <-outcome:
		t.Fatalf("the wheel ended the drop: %+v, %v", got.report, got.err)
	default:
	}
	if owner, err := source.Owner(); err != nil || owner != atomNone {
		t.Errorf("after the wheel XdndSelection is owned by 0x%x, %v; want nobody", owner, err)
	}
	if seen := target.messages(); len(seen) != 0 {
		t.Errorf("after the wheel the target saw %v, want nothing", seen)
	}
	input.moveTo(20, 20)
	input.press(1)
	input.release(1)
	report := waitForDrop(t, outcome)
	if report.Dropped || report.Outcome != "released over no window that takes XDND drops" {
		t.Errorf("report = %+v, want the drag of button 1 to end over nothing", report)
	}
}

func TestEndingADragSendsNothingWhenButtonOneIsNotHeld(t *testing.T) {
	display := startXvfb(t)
	source := openDragsOn(t, display)
	canceller := openDragsOn(t, display)
	if err := canceller.Watch(); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	when, err := source.serverTime()
	if err != nil {
		t.Fatalf("serverTime: %v", err)
	}
	if err := source.offerDrag(URIList([]string{scratchFileURI(t)}), when); err != nil {
		t.Fatalf("offerDrag: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ended := false
	change, err := canceller.EndDragAfter(ctx, 50*time.Millisecond, nil, func() error {
		ended = true
		return nil
	})

	if !errors.Is(err, ErrFirstButtonNotHeld) {
		t.Errorf("EndDragAfter: error = %v, want %v", err, ErrFirstButtonNotHeld)
	}
	if ended {
		t.Error("the keys and the release were sent while no button was held")
	}
	if change.Owner != source.Window() {
		t.Errorf("the drag seen was owned by 0x%x, want 0x%x", change.Owner, source.Window())
	}
}
