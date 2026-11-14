package filestream

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/identity"
	"github.com/rodrigogaraujo/lanhop/internal/transport"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

const patience = 10 * time.Second

type onePeer struct{ peer trust.Peer }

func (o onePeer) Find(fingerprint identity.Fingerprint) (trust.Peer, bool) {
	return o.peer, fingerprint == o.peer.Fingerprint
}

func newIdentity(t *testing.T) *identity.Identity {
	t.Helper()
	machine, err := identity.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	return machine
}

func dataConnections(t *testing.T) (sending, receiving *transport.DataConn) {
	t.Helper()
	central, side := newIdentity(t), newIdentity(t)
	unpaced := transport.NewPace(transport.Unpaced)
	listener, err := transport.Listen("127.0.0.1:0", transport.Config{Identity: central, DataPace: unpaced}, onePeer{trust.Peer{Name: "laptop", Fingerprint: side.Fingerprint}})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	numbers := make(chan int32, 1)
	awaited := make(chan *transport.DataConn, 1)
	failed := make(chan error, 1)
	go func() {
		conn, err := listener.AwaitData(ctx, side.Fingerprint, func(transfer int32) error {
			numbers <- transfer
			return nil
		})
		if err != nil {
			failed <- err
			return
		}
		awaited <- conn
	}()
	var transfer int32
	select {
	case transfer = <-numbers:
	case err := <-failed:
		t.Fatalf("AwaitData: %v", err)
	}
	desk := trust.Peer{Name: "desk", Fingerprint: central.Fingerprint, Address: listener.Addr().String()}
	sending, err = transport.DialData(ctx, transport.Config{Identity: side, DataPace: unpaced}, desk, transfer)
	if err != nil {
		t.Fatalf("DialData: %v", err)
	}
	select {
	case receiving = <-awaited:
	case err := <-failed:
		t.Fatalf("AwaitData: %v", err)
	}
	t.Cleanup(func() {
		sending.Close()
		receiving.Close()
	})
	return sending, receiving
}

type folderOfArrivals struct {
	root string

	mutex    sync.Mutex
	expected int64
	entries  int
	finished bool
	gaveUp   bool
}

type arrivingFile struct {
	*os.File
}

func (f arrivingFile) Whole() error {
	return f.Close()
}

func newFolderOfArrivals(t *testing.T) *folderOfArrivals {
	return &folderOfArrivals{root: t.TempDir()}
}

func (f *folderOfArrivals) Expect(bytes int64, filesAndFolders int) error {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.expected, f.entries = bytes, filesAndFolders
	return nil
}

func (f *folderOfArrivals) Folder(name string) error {
	return os.Mkdir(filepath.Join(f.root, filepath.FromSlash(name)), 0o700)
}

func (f *folderOfArrivals) File(name string, size int64) (IncomingFile, error) {
	file, err := os.OpenFile(filepath.Join(f.root, filepath.FromSlash(name)), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	return arrivingFile{file}, nil
}

func (f *folderOfArrivals) Finish() error {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.finished = true
	return nil
}

func (f *folderOfArrivals) GiveUp() {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.gaveUp = true
	entries, _ := os.ReadDir(f.root)
	for _, e := range entries {
		os.RemoveAll(filepath.Join(f.root, e.Name()))
	}
}

func (f *folderOfArrivals) state() (finished, gaveUp bool) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.finished, f.gaveUp
}

func realTempDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func writeFile(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func treeOf(t *testing.T, root string) map[string]string {
	t.Helper()
	tree := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		name, _ := filepath.Rel(root, path)
		name = filepath.ToSlash(name)
		switch {
		case d.IsDir():
			tree[name+"/"] = ""
		case d.Type().IsRegular():
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			tree[name] = string(content)
		default:
			tree[name+"@"] = d.Type().String()
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("walking %s: %v", root, err)
	}
	return tree
}

func describe(tree map[string]string) string {
	names := make([]string, 0, len(tree))
	for name, content := range tree {
		if len(content) > 20 {
			content = content[:20] + "…"
		}
		names = append(names, name+"="+content)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
