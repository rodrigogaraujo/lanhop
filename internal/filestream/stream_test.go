package filestream

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
	"github.com/rodrigogaraujo/lanhop/internal/transport"
)

func receiveInBackground(conn Source, into Arrivals) <-chan error {
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), patience)
		defer cancel()
		done <- Receive(ctx, conn, into)
	}()
	return done
}

func randomContent(t *testing.T, size int) string {
	t.Helper()
	content := make([]byte, size)
	if _, err := rand.Read(content); err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func TestATreeOfFilesArrivesWholeOverTLS(t *testing.T) {
	root := t.TempDir()
	large := randomContent(t, 300<<10)
	writeFile(t, filepath.Join(root, "report", "summary.txt"), "the summary")
	writeFile(t, filepath.Join(root, "report", "charts", "march.bin"), large)
	writeFile(t, filepath.Join(root, "report", "charts", "empty.txt"), "")
	writeFile(t, filepath.Join(root, "report", "zeta", "last.txt"), "last")
	if err := os.Mkdir(filepath.Join(root, "report", "charts", "nothing"), 0o700); err != nil {
		t.Fatal(err)
	}
	photo := writeFile(t, filepath.Join(root, "camera", "photo.jpg"), "jpeg")
	sending, receiving := dataConnections(t)
	arrivals := newFolderOfArrivals(t)

	done := receiveInBackground(receiving, arrivals)
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	listing, err := Send(ctx, sending, []string{filepath.Join(root, "report"), photo})

	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Receive: %v", err)
	}
	wantListing := Listing{Copy: clipboard.FileCopy{FilesAndFolders: 9, Bytes: int64(len("the summary") + len(large) + len("last") + len("jpeg"))}}
	if listing != wantListing {
		t.Errorf("Send = %+v, want %+v", listing, wantListing)
	}
	want := map[string]string{
		"report/":                 "",
		"report/summary.txt":      "the summary",
		"report/charts/":          "",
		"report/charts/march.bin": large,
		"report/charts/empty.txt": "",
		"report/charts/nothing/":  "",
		"report/zeta/":            "",
		"report/zeta/last.txt":    "last",
		"photo.jpg":               "jpeg",
	}
	if got := treeOf(t, arrivals.root); describe(got) != describe(want) {
		t.Errorf("arrived %s\nwant    %s", describe(got), describe(want))
	}
	if arrivals.expected != wantListing.Copy.Bytes || arrivals.entries != 9 {
		t.Errorf("the arrival folder expected %d bytes in %d entries, want %d in 9", arrivals.expected, arrivals.entries, wantListing.Copy.Bytes)
	}
	if finished, gaveUp := arrivals.state(); !finished || gaveUp {
		t.Errorf("the arrival folder finished %t and gave up %t, want finished only", finished, gaveUp)
	}
}

func TestLinksInsideAFolderStayBehindAndAreCounted(t *testing.T) {
	root := t.TempDir()
	secret := writeFile(t, filepath.Join(root, "secret", "id_ed25519"), "private key")
	writeFile(t, filepath.Join(root, "project", "main.go"), "package main")
	if err := os.Symlink(filepath.Dir(secret), filepath.Join(root, "project", "keys")); err != nil {
		t.Fatal(err)
	}
	sending, receiving := dataConnections(t)
	arrivals := newFolderOfArrivals(t)

	done := receiveInBackground(receiving, arrivals)
	listing, err := Send(context.Background(), sending, []string{filepath.Join(root, "project")})

	if err != nil || listing.Skipped != 1 {
		t.Fatalf("Send = %+v, %v, want 1 skipped", listing, err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Receive: %v", err)
	}
	want := map[string]string{"project/": "", "project/main.go": "package main"}
	if got := treeOf(t, arrivals.root); describe(got) != describe(want) {
		t.Errorf("arrived %s, want %s", describe(got), describe(want))
	}
}

type changingOnFirstWrite struct {
	*transport.DataConn
	once   sync.Once
	change func()
}

func (c *changingOnFirstWrite) Write(data []byte) (int, error) {
	c.once.Do(c.change)
	return c.DataConn.Write(data)
}

func TestAFileThatChangesWhileItIsSentStopsTheTransferAndNothingStays(t *testing.T) {
	root := t.TempDir()
	growing := writeFile(t, filepath.Join(root, "report", "log.txt"), randomContent(t, 200<<10))
	writeFile(t, filepath.Join(root, "report", "notes.txt"), "notes")
	sending, receiving := dataConnections(t)
	arrivals := newFolderOfArrivals(t)
	conn := &changingOnFirstWrite{DataConn: sending, change: func() {
		file, err := os.OpenFile(growing, os.O_WRONLY|os.O_APPEND, 0)
		if err != nil {
			t.Error(err)
			return
		}
		file.WriteString("one more line")
		file.Close()
	}}

	done := receiveInBackground(receiving, arrivals)
	_, err := Send(context.Background(), conn, []string{filepath.Join(root, "report")})
	sending.Close()

	if !errors.Is(err, ErrChanged) {
		t.Errorf("Send of a file that grew while it was sent = %v, want ErrChanged", err)
	}
	if err := <-done; err == nil {
		t.Error("Receive of a transfer stopped in the middle succeeded")
	}
	if _, gaveUp := arrivals.state(); !gaveUp {
		t.Error("the arrival folder was not told to give up")
	}
	if got := treeOf(t, arrivals.root); len(got) != 0 {
		t.Errorf("after the stop the arrival folder holds %s, want nothing", describe(got))
	}
}

func TestAFileListedThenChangedIsNotSent(t *testing.T) {
	root := t.TempDir()
	path := writeFile(t, filepath.Join(root, "notes.txt"), "notes")
	other := writeFile(t, filepath.Join(root, "other.txt"), "other")
	cases := map[string]func(t *testing.T){
		"grown": func(t *testing.T) {
			writeFile(t, path, "notes and more")
		},
		"replaced by a file of the same size and time": func(t *testing.T) {
			listed, _ := os.Stat(path)
			replacement := writeFile(t, path+".new", "NOTES")
			os.Chtimes(replacement, listed.ModTime(), listed.ModTime())
			if err := os.Rename(replacement, path); err != nil {
				t.Fatal(err)
			}
		},
		"replaced by a link": func(t *testing.T) {
			os.Remove(path)
			if err := os.Symlink(other, path); err != nil {
				t.Fatal(err)
			}
		},
		"removed": func(t *testing.T) {
			os.Remove(path)
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			writeFile(t, path, "notes")
			listed, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			change(t)

			_, err = sendFile(&bytes.Buffer{}, entry{path: path, name: "notes.txt", info: listed}, make([]byte, streamBuffer))

			if !errors.Is(err, ErrChanged) {
				t.Errorf("sending a file listed and then %s = %v, want ErrChanged", name, err)
			}
		})
	}
}

func TestAConnectionLostInTheMiddleOfAFileLeavesNothingBehind(t *testing.T) {
	sending, receiving := dataConnections(t)
	arrivals := newFolderOfArrivals(t)

	done := receiveInBackground(receiving, arrivals)
	var stream bytes.Buffer
	writeHeader(&stream, header{filesAndFolders: 2, bytes: 1000})
	writeEntryStart(&stream, entryStart{kind: folderEntry, name: "report"})
	writeEntryStart(&stream, entryStart{kind: fileEntry, name: "report/big.bin", size: 1000})
	stream.Write(make([]byte, 400))
	if _, err := sending.Write(stream.Bytes()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	waitUntil(t, func() bool { return len(treeOf(t, arrivals.root)) == 2 })
	sending.Close()

	err := <-done
	if err == nil {
		t.Fatal("Receive of a connection lost in the middle of a file succeeded")
	}
	if _, gaveUp := arrivals.state(); !gaveUp {
		t.Error("the arrival folder was not told to give up")
	}
	if got := treeOf(t, arrivals.root); len(got) != 0 {
		t.Errorf("after the lost connection the arrival folder holds %s, want nothing", describe(got))
	}
}

func TestSendFailsWhenTheOtherMachineHangsUpBeforeEveryFileArrived(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "notes.txt"), "notes")
	sending, receiving := dataConnections(t)
	go func() {
		buffer := make([]byte, headerBytes)
		receiving.Read(buffer)
		receiving.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	_, err := Send(ctx, sending, []string{filepath.Join(root, "notes.txt")})

	if err == nil {
		t.Error("Send succeeded though the other machine hung up before it held the file")
	}
	if ctx.Err() != nil {
		t.Errorf("Send waited until its context ended: %v", err)
	}
}

func TestReceiveStopsWhenItsContextEnds(t *testing.T) {
	_, receiving := dataConnections(t)
	arrivals := newFolderOfArrivals(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Receive(ctx, receiving, arrivals) }()

	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Receive after its context was cancelled = %v, want context.Canceled", err)
		}
	case <-time.After(patience):
		t.Fatal("Receive went on after its context was cancelled")
	}
	if _, gaveUp := arrivals.state(); !gaveUp {
		t.Error("the arrival folder was not told to give up")
	}
}

func waitUntil(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(patience)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("the condition never held")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
