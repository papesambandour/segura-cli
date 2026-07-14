package recent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRecordUpsertAndFavorite(t *testing.T) {
	s := &Store{}
	s.Record("root", "web01", "10.0.0.1")
	s.Record("root", "web01", "10.0.0.1") // same target again
	if len(s.Entries) != 1 {
		t.Fatalf("expected 1 entry after two records of same target, got %d", len(s.Entries))
	}
	if s.Entries[0].Count != 2 {
		t.Fatalf("expected count 2, got %d", s.Entries[0].Count)
	}

	// case-insensitive key: ROOT@WEB01 is the same target
	s.Record("ROOT", "WEB01", "")
	if len(s.Entries) != 1 {
		t.Fatalf("case-insensitive upsert failed: %d entries", len(s.Entries))
	}
	if s.Entries[0].Count != 3 {
		t.Fatalf("expected count 3, got %d", s.Entries[0].Count)
	}

	if got := s.ToggleFavorite("root", "web01", ""); !got {
		t.Fatal("ToggleFavorite should turn favorite on")
	}
	if got := s.ToggleFavorite("root", "web01", ""); got {
		t.Fatal("ToggleFavorite should turn favorite off")
	}
}

func TestSortedFavoritesThenRecent(t *testing.T) {
	s := &Store{Entries: []Entry{
		{Username: "a", Device: "old", LastUsed: 100},
		{Username: "b", Device: "new", LastUsed: 300},
		{Username: "c", Device: "fav", LastUsed: 50, Favorite: true},
		{Username: "d", Device: "mid", LastUsed: 200},
	}}
	got := s.Sorted()
	order := []string{}
	for _, e := range got {
		order = append(order, e.Device)
	}
	// favorite first, then by LastUsed desc
	want := []string{"fav", "new", "mid", "old"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("sorted order = %v, want %v", order, want)
		}
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	s, err := Load()
	if err != nil {
		t.Fatalf("Load on empty: %v", err)
	}
	s.Record("root", "db01", "10.0.0.9")
	s.SetFavorite("root", "db01", "10.0.0.9", true)
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// file exists with 0600
	p := filepath.Join(tmp, ".segura", "recent.json")
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %v, want 0600", info.Mode().Perm())
	}

	s2, err := Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(s2.Entries) != 1 || !s2.Entries[0].Favorite || s2.Entries[0].IP != "10.0.0.9" {
		t.Fatalf("round-trip mismatch: %+v", s2.Entries)
	}
}

func TestRemove(t *testing.T) {
	s := &Store{}
	s.Record("root", "x", "")
	if !s.Remove("root", "x") {
		t.Fatal("Remove should return true")
	}
	if s.Remove("root", "x") {
		t.Fatal("Remove of missing should return false")
	}
	if len(s.Entries) != 0 {
		t.Fatalf("expected empty, got %d", len(s.Entries))
	}
}
