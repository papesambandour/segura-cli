// Package recent persists recently-used and favorited connection targets so the
// interactive picker can surface them first. Stored at ~/.segura/recent.json.
package recent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Entry is one remembered target (a credential on a device).
type Entry struct {
	Username string `json:"username"`
	Device   string `json:"device"`
	IP       string `json:"ip,omitempty"`
	Count    int    `json:"count"`
	LastUsed int64  `json:"lastUsed"` // unix seconds
	Favorite bool   `json:"favorite,omitempty"`
}

// Key identifies an entry regardless of stored IP.
func (e Entry) Key() string { return key(e.Username, e.Device) }

func key(username, device string) string {
	return strings.ToLower(username) + "@" + strings.ToLower(device)
}

// Store is the in-memory view of the recents file.
type Store struct {
	path    string
	Entries []Entry `json:"entries"`
}

// storePath returns ~/.segura/recent.json.
func storePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".segura", "recent.json"), nil
}

// Load reads the store, returning an empty (usable) store if the file is absent.
func Load() (*Store, error) {
	p, err := storePath()
	if err != nil {
		return nil, err
	}
	s := &Store{path: p}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	// Tolerate an empty/corrupt file: start fresh rather than erroring.
	_ = json.Unmarshal(data, s)
	return s, nil
}

// index returns the position of username@device, or -1.
func (s *Store) index(username, device string) int {
	k := key(username, device)
	for i, e := range s.Entries {
		if e.Key() == k {
			return i
		}
	}
	return -1
}

// Record upserts a connection: bumps the count and last-used timestamp.
func (s *Store) Record(username, device, ip string) {
	now := time.Now().Unix()
	if i := s.index(username, device); i >= 0 {
		s.Entries[i].Count++
		s.Entries[i].LastUsed = now
		if ip != "" {
			s.Entries[i].IP = ip
		}
		return
	}
	s.Entries = append(s.Entries, Entry{
		Username: username, Device: device, IP: ip, Count: 1, LastUsed: now,
	})
}

// SetFavorite marks (or unmarks) a target as favorite, creating it if needed.
func (s *Store) SetFavorite(username, device, ip string, fav bool) {
	if i := s.index(username, device); i >= 0 {
		s.Entries[i].Favorite = fav
		if ip != "" {
			s.Entries[i].IP = ip
		}
		return
	}
	if fav {
		s.Entries = append(s.Entries, Entry{
			Username: username, Device: device, IP: ip, Favorite: true, LastUsed: time.Now().Unix(),
		})
	}
}

// ToggleFavorite flips the favorite flag and returns the new state.
func (s *Store) ToggleFavorite(username, device, ip string) bool {
	i := s.index(username, device)
	if i < 0 {
		s.SetFavorite(username, device, ip, true)
		return true
	}
	s.Entries[i].Favorite = !s.Entries[i].Favorite
	return s.Entries[i].Favorite
}

// Remove deletes a target. Returns true if something was removed.
func (s *Store) Remove(username, device string) bool {
	i := s.index(username, device)
	if i < 0 {
		return false
	}
	s.Entries = append(s.Entries[:i], s.Entries[i+1:]...)
	return true
}

// Sorted returns entries ordered favorites-first, then most-recent-first.
func (s *Store) Sorted() []Entry {
	out := make([]Entry, len(s.Entries))
	copy(out, s.Entries)
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Favorite != out[b].Favorite {
			return out[a].Favorite // favorites first
		}
		return out[a].LastUsed > out[b].LastUsed // newer first
	})
	return out
}

// Save writes the store atomically with 0600 perms (creating ~/.segura if needed).
func (s *Store) Save() error {
	if s.path == "" {
		p, err := storePath()
		if err != nil {
			return err
		}
		s.path = p
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
