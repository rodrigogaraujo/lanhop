package trust

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/rodrigogaraujo/lanhop/internal/identity"
)

func fingerprint(seed byte) identity.Fingerprint {
	var value identity.Fingerprint
	value[0] = seed
	return value
}

func openStore(t *testing.T, path string) *Store {
	t.Helper()
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return store
}

func addPeer(t *testing.T, store *Store, peer Peer) {
	t.Helper()
	if err := store.Add(peer); err != nil {
		t.Fatalf("Add(%+v): %v", peer, err)
	}
}

func TestStoreWithoutAFileIsEmpty(t *testing.T) {
	store := openStore(t, filepath.Join(t.TempDir(), "peers.json"))
	if peers := store.Peers(); len(peers) != 0 {
		t.Errorf("Peers = %+v, want none", peers)
	}
}

func TestAddedPeersSurviveReopeningInTheSameOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lanhop", "peers.json")
	laptop := Peer{Name: "laptop", Fingerprint: fingerprint(1)}
	desktop := Peer{Name: "desktop", Fingerprint: fingerprint(2), Address: "192.168.0.10:24745"}

	store := openStore(t, path)
	addPeer(t, store, laptop)
	addPeer(t, store, desktop)

	if got, want := openStore(t, path).Peers(), []Peer{laptop, desktop}; !slices.Equal(got, want) {
		t.Errorf("Peers after reopening = %+v, want %+v", got, want)
	}
}

func TestFindLooksUpByFingerprint(t *testing.T) {
	store := openStore(t, filepath.Join(t.TempDir(), "peers.json"))
	laptop := Peer{Name: "laptop", Fingerprint: fingerprint(1)}
	addPeer(t, store, laptop)

	if got, found := store.Find(fingerprint(1)); !found || got != laptop {
		t.Errorf("Find of a trusted fingerprint = %+v, %v", got, found)
	}
	if _, found := store.Find(fingerprint(9)); found {
		t.Error("Find of an unknown fingerprint reported a peer")
	}
}

func TestRemovedPeerIsNoLongerTrusted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peers.json")
	store := openStore(t, path)
	addPeer(t, store, Peer{Name: "laptop", Fingerprint: fingerprint(1)})
	addPeer(t, store, Peer{Name: "desktop", Fingerprint: fingerprint(2)})

	if err := store.Remove("laptop"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	for _, current := range []*Store{store, openStore(t, path)} {
		if _, found := current.Find(fingerprint(1)); found {
			t.Error("the removed peer is still trusted")
		}
		if _, found := current.Find(fingerprint(2)); !found {
			t.Error("the other peer was lost")
		}
	}
}

func TestRemovingAnUnknownNameFails(t *testing.T) {
	store := openStore(t, filepath.Join(t.TempDir(), "peers.json"))
	if err := store.Remove("ghost"); err == nil {
		t.Error("Remove of an unknown name succeeded")
	}
}

func TestPeersThatWouldBeAmbiguousAreRejected(t *testing.T) {
	cases := map[string]Peer{
		"repeated name":         {Name: "laptop", Fingerprint: fingerprint(2)},
		"repeated fingerprint":  {Name: "other", Fingerprint: fingerprint(1)},
		"empty name":            {Name: "", Fingerprint: fingerprint(3)},
		"name with a space":     {Name: "my laptop", Fingerprint: fingerprint(3)},
		"name that is a dot":    {Name: ".", Fingerprint: fingerprint(3)},
		"name that is too long": {Name: strings.Repeat("a", 33), Fingerprint: fingerprint(3)},
	}
	for name, peer := range cases {
		t.Run(name, func(t *testing.T) {
			store := openStore(t, filepath.Join(t.TempDir(), "peers.json"))
			addPeer(t, store, Peer{Name: "laptop", Fingerprint: fingerprint(1)})

			if err := store.Add(peer); err == nil {
				t.Errorf("Add(%+v) succeeded", peer)
			}
			if got := len(store.Peers()); got != 1 {
				t.Errorf("store has %d peers after a rejected Add, want 1", got)
			}
		})
	}
}

func TestStoreFileIsPrivateToItsOwner(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are not enforced on Windows")
	}
	path := filepath.Join(t.TempDir(), "peers.json")
	addPeer(t, openStore(t, path), Peer{Name: "laptop", Fingerprint: fingerprint(1)})

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %04o, want 0600", got)
	}
}

func TestDamagedFileIsReportedAndLeftAlone(t *testing.T) {
	for name, contents := range map[string]string{
		"not json":          "{peers",
		"bad fingerprint":   `{"peers":[{"name":"laptop","fingerprint":"abcd"}]}`,
		"repeated name":     `{"peers":[{"name":"a","fingerprint":"` + fingerprint(1).String() + `"},{"name":"a","fingerprint":"` + fingerprint(2).String() + `"}]}`,
		"name with a space": `{"peers":[{"name":"my laptop","fingerprint":"` + fingerprint(1).String() + `"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "peers.json")
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}

			if _, err := Open(path); err == nil || !strings.Contains(err.Error(), "damaged") {
				t.Errorf("Open = %v, want an error that says the file is damaged", err)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}
			if string(after) != contents {
				t.Error("the damaged file was changed")
			}
		})
	}
}

func TestSavingLeavesNoTemporaryFilesBehind(t *testing.T) {
	directory := t.TempDir()
	store := openStore(t, filepath.Join(directory, "peers.json"))
	addPeer(t, store, Peer{Name: "laptop", Fingerprint: fingerprint(1)})
	addPeer(t, store, Peer{Name: "desktop", Fingerprint: fingerprint(2)})

	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "peers.json" {
		t.Errorf("directory holds %v, want only peers.json", entries)
	}
}
