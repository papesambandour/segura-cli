// Package picker renders an interactive fuzzy-search list of connection targets,
// with favorites and recently-used entries surfaced first.
package picker

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ktr0731/go-fuzzyfinder"

	"segura-cli/internal/recent"
	"segura-cli/internal/webproxy"
)

// ErrCancelled is returned when the user aborts the picker (Esc / Ctrl-C).
var ErrCancelled = errors.New("selection cancelled")

// Item is one selectable target, enriched with favorite/recent metadata.
type Item struct {
	Username string
	Device   string
	IP       string
	Favorite bool
	LastUsed int64
	Count    int
}

func itemKey(username, device string) string {
	return strings.ToLower(username) + "@" + strings.ToLower(device)
}

// Build merges the live credential list with the recents/favorites store into a
// single ordered slice: favorites first, then most-recent, then the rest A→Z.
func Build(creds []webproxy.Credential, store *recent.Store) []Item {
	byKey := map[string]*Item{}
	var items []*Item

	add := func(username, device, ip string) *Item {
		k := itemKey(username, device)
		if it, ok := byKey[k]; ok {
			if it.IP == "" && ip != "" {
				it.IP = ip
			}
			return it
		}
		it := &Item{Username: username, Device: device, IP: ip}
		byKey[k] = it
		items = append(items, it)
		return it
	}

	for _, c := range creds {
		add(c.Username, c.Device, c.IP)
	}
	if store != nil {
		for _, e := range store.Entries {
			it := add(e.Username, e.Device, e.IP)
			it.Favorite = e.Favorite
			it.LastUsed = e.LastUsed
			it.Count = e.Count
		}
	}

	sort.SliceStable(items, func(a, b int) bool {
		ia, ib := items[a], items[b]
		if ia.Favorite != ib.Favorite {
			return ia.Favorite
		}
		if ia.LastUsed != ib.LastUsed {
			return ia.LastUsed > ib.LastUsed
		}
		return itemKey(ia.Username, ia.Device) < itemKey(ib.Username, ib.Device)
	})

	out := make([]Item, len(items))
	for i, it := range items {
		out[i] = *it
	}
	return out
}

// label is the row shown in the finder (also the fuzzy-search haystack).
func label(it Item) string {
	marker := "  "
	switch {
	case it.Favorite:
		marker = "★ "
	case it.LastUsed > 0:
		marker = "• "
	}
	s := fmt.Sprintf("%s%s@%s", marker, it.Username, it.Device)
	if it.IP != "" && !strings.EqualFold(it.IP, it.Device) {
		s += "  (" + it.IP + ")"
	}
	return s
}

// preview is the detail pane for the highlighted row.
func preview(it Item) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Credential : %s\n", it.Username)
	fmt.Fprintf(&b, "Device     : %s\n", it.Device)
	if it.IP != "" {
		fmt.Fprintf(&b, "IP         : %s\n", it.IP)
	}
	if it.Favorite {
		fmt.Fprintf(&b, "\n★ Favorite\n")
	}
	if it.LastUsed > 0 {
		fmt.Fprintf(&b, "Last used  : %s\n", time.Unix(it.LastUsed, 0).Format("2006-01-02 15:04"))
		fmt.Fprintf(&b, "Times used : %d\n", it.Count)
	}
	return b.String()
}

// Select shows the fuzzy finder over items (optionally pre-filtered by query) and
// returns the chosen item. Returns ErrCancelled if the user aborts.
func Select(items []Item, query string) (Item, error) {
	if len(items) == 0 {
		return Item{}, errors.New("no credentials available to pick from")
	}
	opts := []fuzzyfinder.Option{
		fuzzyfinder.WithPreviewWindow(func(i, _, _ int) string {
			if i < 0 || i >= len(items) {
				return ""
			}
			return preview(items[i])
		}),
	}
	if query != "" {
		opts = append(opts, fuzzyfinder.WithQuery(query))
	}
	idx, err := fuzzyfinder.Find(items, func(i int) string { return label(items[i]) }, opts...)
	if err != nil {
		if errors.Is(err, fuzzyfinder.ErrAbort) {
			return Item{}, ErrCancelled
		}
		return Item{}, err
	}
	return items[idx], nil
}
