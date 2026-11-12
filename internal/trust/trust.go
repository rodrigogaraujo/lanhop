package trust

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"

	"github.com/rodrigogaraujo/lanhop/internal/identity"
)

const (
	directoryMode = 0o700
	fileMode      = 0o600
)

var validName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

type Peer struct {
	Name        string               `json:"name"`
	Fingerprint identity.Fingerprint `json:"fingerprint"`
	Address     string               `json:"address,omitempty"`
}

type Store struct {
	path  string
	peers []Peer
}

type storedFile struct {
	Peers []Peer `json:"peers"`
}

func Open(path string) (*Store, error) {
	store := &Store{path: path}
	contents, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	var stored storedFile
	if err := json.Unmarshal(contents, &stored); err != nil {
		return nil, fmt.Errorf("trust: %s is damaged: %w", path, err)
	}
	for _, peer := range stored.Peers {
		if err := store.check(peer); err != nil {
			return nil, fmt.Errorf("trust: %s is damaged: %w", path, err)
		}
		store.peers = append(store.peers, peer)
	}
	return store, nil
}

func (s *Store) Peers() []Peer {
	return slices.Clone(s.peers)
}

func (s *Store) Find(fingerprint identity.Fingerprint) (Peer, bool) {
	for _, peer := range s.peers {
		if peer.Fingerprint == fingerprint {
			return peer, true
		}
	}
	return Peer{}, false
}

func (s *Store) Add(peer Peer) error {
	if err := s.check(peer); err != nil {
		return err
	}
	return s.replace(append(s.Peers(), peer))
}

func (s *Store) Remove(name string) error {
	remaining := slices.DeleteFunc(s.Peers(), func(peer Peer) bool { return peer.Name == name })
	if len(remaining) == len(s.peers) {
		return fmt.Errorf("trust: no machine is named %q", name)
	}
	return s.replace(remaining)
}

func (s *Store) check(candidate Peer) error {
	if !validName.MatchString(candidate.Name) {
		return fmt.Errorf("trust: name %q must have 1 to 32 letters, digits, hyphens or underscores", candidate.Name)
	}
	for _, peer := range s.peers {
		if peer.Name == candidate.Name {
			return fmt.Errorf("trust: a machine named %q is already trusted", candidate.Name)
		}
		if peer.Fingerprint == candidate.Fingerprint {
			return fmt.Errorf("trust: this machine is already trusted as %q", peer.Name)
		}
	}
	return nil
}

func (s *Store) replace(peers []Peer) error {
	contents, err := json.MarshalIndent(storedFile{Peers: peers}, "", "  ")
	if err != nil {
		return err
	}
	directory := filepath.Dir(s.path)
	if err := os.MkdirAll(directory, directoryMode); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, filepath.Base(s.path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if err := temporary.Chmod(fileMode); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(append(contents, '\n')); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporary.Name(), s.path); err != nil {
		return err
	}
	s.peers = peers
	return nil
}
