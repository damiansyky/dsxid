// Package approvals stores a local list of websites that received a DsxID
// signature. It is a user-facing history, not an authorization database; the
// actual website session remains on the website.
package approvals

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const (
	fileVersion = 1
	fileName    = "approved.json"
)

// Entry is one approved website origin for one identity.
type Entry struct {
	Origin          string    `json:"origin"`
	KeyID           string    `json:"key_id"`
	FirstApprovedAt time.Time `json:"first_approved_at"`
	LastApprovedAt  time.Time `json:"last_approved_at"`
	SignIns         int       `json:"sign_ins"`
}

type persisted struct {
	Version int     `json:"version"`
	Entries []Entry `json:"entries"`
}

// Store is a small concurrency-safe JSON store.
type Store struct {
	mu      sync.Mutex
	path    string
	entries map[string]Entry
}

// Open loads the approved-sites history from dir. A brand new store is empty.
func Open(dir string) (*Store, error) {
	s := &Store{
		path:    filepath.Join(dir, fileName),
		entries: make(map[string]Entry),
	}
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var p persisted
	if err := json.Unmarshal(raw, &p); err != nil || p.Version != fileVersion {
		return nil, fmt.Errorf("approved accounts file is corrupted")
	}
	for _, entry := range p.Entries {
		if entry.Origin == "" || entry.KeyID == "" || entry.SignIns < 1 {
			continue
		}
		s.entries[entryKey(entry.KeyID, entry.Origin)] = entry
	}
	return s, nil
}

// Record notes a successful signature for a website origin.
func (s *Store) Record(keyID, origin string, at time.Time) error {
	if keyID == "" || origin == "" {
		return errors.New("missing identity or website origin")
	}
	at = at.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	key := entryKey(keyID, origin)
	entry, exists := s.entries[key]
	if !exists {
		entry = Entry{
			Origin:          origin,
			KeyID:           keyID,
			FirstApprovedAt: at,
		}
	}
	entry.LastApprovedAt = at
	entry.SignIns++
	s.entries[key] = entry
	return s.saveLocked()
}

// List returns approved websites for keyID, newest approval first.
func (s *Store) List(keyID string) []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Entry, 0)
	for _, entry := range s.entries {
		if entry.KeyID == keyID {
			out = append(out, entry)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].LastApprovedAt.After(out[j].LastApprovedAt)
	})
	return out
}

// ForgetKey removes all history for an identity.
func (s *Store) ForgetKey(keyID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for key, entry := range s.entries {
		if entry.KeyID == keyID {
			delete(s.entries, key)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	entries := make([]Entry, 0, len(s.entries))
	for _, entry := range s.entries {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].LastApprovedAt.After(entries[j].LastApprovedAt)
	})
	raw, err := json.MarshalIndent(persisted{Version: fileVersion, Entries: entries}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".approved-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

func entryKey(keyID, origin string) string {
	return keyID + "\x1f" + origin
}
