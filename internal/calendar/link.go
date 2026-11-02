package calendar

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Kenttleton/focacal/internal/config"
	"github.com/Kenttleton/focacal/internal/store"
)

// Link groups store.Event rows describing the same meeting.
type Link struct {
	Key       string
	Heuristic bool
	Members   []*store.Event
	Canonical *store.Event
}

// SourceOrder returns a map of source id -> declaration index.
func SourceOrder(uc *config.UserConfig) map[string]int {
	m := make(map[string]int)
	for i, s := range uc.Sources {
		m[s.ID] = i
	}
	return m
}

func normTitle(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}

// Group links events into Links.
func Group(events []*store.Event, order map[string]int) []Link {
	// Primary key grouping.
	groups := make(map[string][]*store.Event)
	var keys []string
	for _, e := range events {
		var k string
		if e.ICalUID == "" {
			k = "row|" + e.ID
		} else {
			k = fmt.Sprintf("ical|%s|%d", e.ICalUID, e.Start.Unix())
		}
		if _, ok := groups[k]; !ok {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], e)
	}

	var links []Link
	for _, k := range keys {
		members := groups[k]
		canonical := pickCanonical(members, order)
		links = append(links, Link{Key: k, Members: members, Canonical: canonical})
	}

	// Second pass: merge links when heuristic conditions hold.
	merged := true
	for merged {
		merged = false
		for i := 0; i < len(links); i++ {
			for j := i + 1; j < len(links); j++ {
				if canMerge(links[i], links[j]) {
					// merge j into i
					links[i].Members = append(links[i].Members, links[j].Members...)
					links[i].Heuristic = true
					links[i].Canonical = pickCanonical(links[i].Members, order)
					links = append(links[:j], links[j+1:]...)
					merged = true
					break
				}
			}
			if merged {
				break
			}
		}
	}
	return links
}

func canMerge(a, b Link) bool {
	// members from different sources
	srcs := make(map[string]bool)
	for _, m := range a.Members {
		srcs[m.SourceID] = true
	}
	for _, m := range b.Members {
		if srcs[m.SourceID] {
			return false
		}
	}
	// need exactly? just different sources overall; ensure no shared source
	// identical start and end
	ca, cb := a.Canonical, b.Canonical
	if !ca.Start.Equal(cb.Start) || !ca.End.Equal(cb.End) {
		return false
	}
	// equal normalised title, non-empty
	ta, tb := normTitle(ca.Title), normTitle(cb.Title)
	if ta == "" || ta != tb {
		return false
	}
	// neither side all-day
	if ca.AllDay || cb.AllDay {
		return false
	}
	return true
}

func pickCanonical(members []*store.Event, order map[string]int) *store.Event {
	sorted := append([]*store.Event(nil), members...)
	sort.Slice(sorted, func(i, j int) bool {
		oi, oj := order[sorted[i].SourceID], order[sorted[j].SourceID]
		// missing source -> large
		if _, ok := order[sorted[i].SourceID]; !ok {
			oi = 1 << 30
		}
		if _, ok := order[sorted[j].SourceID]; !ok {
			oj = 1 << 30
		}
		if oi != oj {
			return oi < oj
		}
		if sorted[i].CalendarID != sorted[j].CalendarID {
			return sorted[i].CalendarID < sorted[j].CalendarID
		}
		return sorted[i].ID < sorted[j].ID
	})
	return sorted[0]
}
