package clipshare

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/arrival"
	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/features"
	"github.com/rodrigogaraujo/lanhop/internal/transport"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

const (
	oneMebibytePerSecond = 1 << 20
	arrivingPrefix       = ".lanhop-arriving-"
	textAndFiles         = event.FeatureClipboard | 1<<features.Files
)

type timedFileWrite struct {
	paths []string
	at    time.Time
}

type takingFiles struct {
	*fakeClipboard
}

func (b takingFiles) ReadFiles() ([]string, error) {
	return b.readFiles()
}

func (b takingFiles) WriteFiles(paths []string) (int64, error) {
	return b.writeFiles(paths)
}

func (f *fakeClipboard) personCopiesFiles(paths ...string) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.text, f.paths, f.refusal = "", slices.Clone(paths), nil
	f.mark++
}

func (f *fakeClipboard) readFiles() ([]string, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	switch {
	case errors.Is(f.refusal, clipboard.ErrPrivate):
		return nil, f.refusal
	case f.refusal != nil || f.paths == nil:
		return nil, clipboard.ErrNotFiles
	}
	return slices.Clone(f.paths), nil
}

func (f *fakeClipboard) writeFiles(paths []string) (int64, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.text, f.paths, f.refusal = "", slices.Clone(paths), nil
	f.mark++
	f.fileWrites = append(f.fileWrites, timedFileWrite{paths: slices.Clone(paths), at: time.Now()})
	return f.mark, nil
}

func (f *fakeClipboard) filesWritten() []timedFileWrite {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return slices.Clone(f.fileWrites)
}

func newFileDesk(t *testing.T, sideNames ...string) *deskUnderTest {
	t.Helper()
	board := holding("what the central held before")
	return newDeskWith(t, deskSetup{board: board, open: opening(takingFiles{board}), files: true}, sideNames...)
}

func newSlowFileDesk(t *testing.T, bytesPerSecond int, sideNames ...string) *deskUnderTest {
	t.Helper()
	board := holding("what the central held before")
	return newDeskWith(t, deskSetup{board: board, open: opening(takingFiles{board}), files: true, bytesPerSecond: bytesPerSecond}, sideNames...)
}

func openArrivals(t *testing.T, room int64) (string, *arrival.Folder) {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "arrived")
	folder, err := arrival.Open(arrival.Config{Directory: directory, Room: room, Margin: 1})
	if err != nil {
		t.Fatalf("arrival.Open: %v", err)
	}
	t.Cleanup(func() { folder.Close() })
	return directory, folder
}

func (d *deskUnderTest) dataDialerOf(side *sideUnderTest) DialData {
	dialing := transportFor(side.identity)
	dialing.DataPace = side.pace
	address := d.address
	if side.dataAddress != "" {
		address = side.dataAddress
	}
	central := trust.Peer{Name: "desk", Fingerprint: d.identity.Fingerprint, Address: address}
	return func(ctx context.Context, transfer int32) (*transport.DataConn, error) {
		return transport.DialData(ctx, dialing, central, transfer)
	}
}

func (d *deskUnderTest) sideJoinsWithoutFiles(name string) *sideUnderTest {
	d.t.Helper()
	side := d.sides[name]
	d.startSide(side, d.address, opening(side.clipboard))
	return side
}

func (d *deskUnderTest) waitUntilSharingFiles(sides ...*sideUnderTest) {
	d.t.Helper()
	d.waitUntilTakingPart(sides...)
	for _, side := range sides {
		eventually(d.t, "the central sharing files with "+side.name, func() bool {
			return slices.Contains(namesSharingFiles(d.t, d.runtime), side.name)
		})
	}
}

func namesSharingFiles(t *testing.T, c *Central) []string {
	t.Helper()
	answer := make(chan []string, 1)
	c.tell(func() {
		var names []string
		for l, machine := range c.sides {
			if (machine.takes & c.on).Has(clipboard.Files) {
				names = append(names, l.name)
			}
		}
		answer <- names
	})
	select {
	case names := <-answer:
		return names
	case <-time.After(testPatience):
		t.Fatal("the central runtime did not answer")
		return nil
	}
}

func randomContent(t *testing.T, size int) []byte {
	t.Helper()
	content := make([]byte, size)
	if _, err := rand.Read(content); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return content
}

func writeFile(t *testing.T, path string, content []byte) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func makeFolder(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	return path
}

func waitUntilFilesWritten(t *testing.T, name string, board *fakeClipboard, writes int) []string {
	t.Helper()
	eventually(t, name+" getting files on its clipboard", func() bool { return len(board.filesWritten()) >= writes })
	written := board.filesWritten()
	return written[writes-1].paths
}

func expectNoFilesWritten(t *testing.T, name string, board *fakeClipboard) {
	t.Helper()
	if writes := board.filesWritten(); len(writes) > 0 {
		t.Errorf("%s got files on its clipboard %d times, first %q, want none", name, len(writes), writes[0].paths)
	}
}

type treeEntry struct {
	folder bool
	digest [sha256.Size]byte
}

func treeOf(t *testing.T, top string) map[string]treeEntry {
	t.Helper()
	tree := map[string]treeEntry{}
	err := filepath.WalkDir(top, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(filepath.Dir(top), path)
		if err != nil {
			return err
		}
		switch {
		case entry.IsDir():
			tree[relative] = treeEntry{folder: true}
		case entry.Type().IsRegular():
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			tree[relative] = treeEntry{digest: sha256.Sum256(content)}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", top, err)
	}
	return tree
}

func expectSameFiles(t *testing.T, arrived []string, inside string, sources ...string) {
	t.Helper()
	if len(arrived) != len(sources) {
		t.Fatalf("%d paths arrived, want %d: %q", len(arrived), len(sources), arrived)
	}
	for i, source := range sources {
		if !strings.HasPrefix(arrived[i], inside+string(filepath.Separator)) {
			t.Errorf("%q arrived outside the arrival folder %q", arrived[i], inside)
		}
		if filepath.Base(arrived[i]) != filepath.Base(source) {
			t.Errorf("%q arrived as %q", source, arrived[i])
			continue
		}
		want, got := treeOf(t, source), treeOf(t, arrived[i])
		if len(want) != len(got) {
			t.Errorf("%q arrived with %d files and folders, want %d", source, len(got), len(want))
		}
		for name, entry := range want {
			if got[name] != entry {
				t.Errorf("%s did not arrive whole and the same", name)
			}
		}
	}
}

func arrivalsLeftIn(t *testing.T, directory string) []string {
	t.Helper()
	listed, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var left []string
	for _, entry := range listed {
		if entry.Name() != ".lock" {
			left = append(left, entry.Name())
		}
	}
	return left
}

func waitForAnyLog(t *testing.T, wanted string, logs ...*logBook) {
	t.Helper()
	eventually(t, "the log line "+wanted, func() bool {
		for _, log := range logs {
			if strings.Contains(log.String(), wanted) {
				return true
			}
		}
		return false
	})
}

func expectNothingLeftIn(t *testing.T, name, directory string) {
	t.Helper()
	eventually(t, "nothing left in the arrival folder of "+name, func() bool { return len(arrivalsLeftIn(t, directory)) == 0 })
}

func waitUntilArriving(t *testing.T, name, directory string) {
	t.Helper()
	eventually(t, "files starting to arrive on "+name, func() bool {
		found := false
		filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
			if err != nil || found {
				return nil
			}
			if strings.HasPrefix(entry.Name(), arrivingPrefix) {
				if info, err := entry.Info(); err == nil && info.Size() > 0 {
					found = true
				}
			}
			return nil
		})
		return found
	})
}

type cuttableProxy struct {
	address string
	mutex   sync.Mutex
	open    []net.Conn
}

func cuttableProxyTo(t *testing.T, target string) *cuttableProxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	proxy := &cuttableProxy{address: listener.Addr().String()}
	go func() {
		for {
			near, err := listener.Accept()
			if err != nil {
				return
			}
			far, err := net.Dial("tcp", target)
			if err != nil {
				near.Close()
				continue
			}
			proxy.mutex.Lock()
			proxy.open = append(proxy.open, near, far)
			proxy.mutex.Unlock()
			go forward(near, far)
			go forward(far, near)
		}
	}()
	t.Cleanup(proxy.cut)
	return proxy
}

func forward(to, from net.Conn) {
	io.Copy(to, from)
	to.Close()
	from.Close()
}

func (p *cuttableProxy) cut() {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	for _, conn := range p.open {
		conn.Close()
	}
	p.open = nil
}

type rogueSide struct {
	conn    *transport.Conn
	dialing transport.Config
	central trust.Peer
	mutex   sync.Mutex
	names   []string
}

func (d *deskUnderTest) rogueSendsFilesNamed(name string, names ...string) *rogueSide {
	d.t.Helper()
	side := d.sides[name]
	dialing := transportFor(side.identity)
	dialing.DataPace = transport.NewPace(0)
	rogue := &rogueSide{
		conn:    d.dialAs(name),
		dialing: dialing,
		central: trust.Peer{Name: "desk", Fingerprint: d.identity.Fingerprint, Address: d.address},
		names:   names,
	}
	if err := rogue.conn.Send(event.Features(textAndFiles)); err != nil {
		d.t.Fatalf("Send: %v", err)
	}
	go rogue.answer()
	d.waitUntilConnected(name)
	return rogue
}

func (r *rogueSide) answer() {
	for {
		frame, err := r.conn.Receive()
		if err != nil {
			return
		}
		switch {
		case frame == event.Leave():
			if r.conn.Send(event.FileCopy(1), event.FileCopySize(1)) != nil {
				return
			}
		case frame.IsControl() && frame.Code == event.ControlOpenData:
			if wire, sideSends := event.SplitOpenData(frame.Value); sideSends {
				go r.sendUnder(wire, r.nextName())
			}
		}
	}
}

func (r *rogueSide) nextName() string {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if len(r.names) == 0 {
		return "nothing more"
	}
	name := r.names[0]
	r.names = r.names[1:]
	return name
}

func (r *rogueSide) sendUnder(wire int32, name string) {
	ctx, cancel := context.WithTimeout(context.Background(), testPatience)
	defer cancel()
	conn, err := transport.DialData(ctx, r.dialing, r.central, wire)
	if err != nil {
		return
	}
	defer conn.Close()
	content := []byte("escaped")
	digest := sha256.Sum256(content)
	var stream bytes.Buffer
	stream.WriteByte(1)
	stream.Write(binary.BigEndian.AppendUint32(nil, 1))
	stream.Write(binary.BigEndian.AppendUint64(nil, uint64(len(content))))
	stream.WriteByte(1)
	stream.Write(binary.BigEndian.AppendUint16(nil, uint16(len(name))))
	stream.WriteString(name)
	stream.Write(binary.BigEndian.AppendUint64(nil, uint64(len(content))))
	stream.Write(content)
	stream.Write(digest[:])
	stream.WriteByte(0)
	if _, err := conn.Write(stream.Bytes()); err != nil {
		return
	}
	conn.AwaitAllArrived(ctx)
}
