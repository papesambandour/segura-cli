package picker

import (
	"strings"
	"testing"

	"segura-cli/internal/recent"
	"segura-cli/internal/webproxy"
)

func TestBuildMergesAndOrders(t *testing.T) {
	creds := []webproxy.Credential{
		{Username: "root", Device: "alpha", IP: "10.0.0.1"},
		{Username: "root", Device: "beta", IP: "10.0.0.2"},
		{Username: "admin", Device: "gamma", IP: "10.0.0.3"},
	}
	store := &recent.Store{Entries: []recent.Entry{
		{Username: "admin", Device: "gamma", LastUsed: 500, Favorite: true, Count: 9},
		{Username: "root", Device: "beta", LastUsed: 200, Count: 3},
	}}

	items := Build(creds, store)
	if len(items) != 3 {
		t.Fatalf("expected 3 merged items, got %d", len(items))
	}

	// favorite (admin@gamma) first, then recent (root@beta), then the rest (root@alpha)
	want := []string{"gamma", "beta", "alpha"}
	for i, w := range want {
		if items[i].Device != w {
			got := []string{}
			for _, it := range items {
				got = append(got, it.Device)
			}
			t.Fatalf("order = %v, want %v", got, want)
		}
	}

	// metadata overlaid from the store
	if !items[0].Favorite || items[0].Count != 9 {
		t.Fatalf("favorite metadata not overlaid: %+v", items[0])
	}
	// IP preserved from creds even when store entry had none
	if items[0].IP != "10.0.0.3" {
		t.Fatalf("IP should come from creds: %q", items[0].IP)
	}
}

func TestBuildIncludesRecentNotInCreds(t *testing.T) {
	// A favorite that no longer appears in the live list must still be selectable.
	creds := []webproxy.Credential{{Username: "root", Device: "alpha", IP: "10.0.0.1"}}
	store := &recent.Store{Entries: []recent.Entry{
		{Username: "old", Device: "ghost", IP: "10.9.9.9", Favorite: true, LastUsed: 999},
	}}
	items := Build(creds, store)
	if len(items) != 2 {
		t.Fatalf("expected 2 items (cred + orphan favorite), got %d", len(items))
	}
	if items[0].Device != "ghost" {
		t.Fatalf("orphan favorite should sort first, got %q", items[0].Device)
	}
}

func TestLabelMarkers(t *testing.T) {
	fav := label(Item{Username: "u", Device: "d", Favorite: true})
	rec := label(Item{Username: "u", Device: "d", LastUsed: 10})
	plain := label(Item{Username: "u", Device: "d"})
	if !strings.HasPrefix(fav, "★") {
		t.Fatalf("favorite marker missing: %q", fav)
	}
	if !strings.HasPrefix(rec, "•") {
		t.Fatalf("recent marker missing: %q", rec)
	}
	if !strings.HasPrefix(plain, "  ") {
		t.Fatalf("plain row should start with spaces: %q", plain)
	}
}
