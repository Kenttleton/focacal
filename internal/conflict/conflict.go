package conflict

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"time"

	"strings"

	"github.com/Kenttleton/focacal/internal/calendar"
	"github.com/Kenttleton/focacal/internal/config"
	"github.com/Kenttleton/focacal/internal/store"
)

// Conflict represents an overlap between two real meetings.
type Conflict struct {
	Key       string
	A, B      calendar.Link
	Tentative bool
	Dismissed bool
}

// Positive reports whether an event counts as a conflict side under its source config.
func Positive(e *store.Event, cfg config.ConflictConfig) bool {
	if e.Status == "declined" || e.ResponseStatus == "declined" {
		if !cfg.CountsDeclined() {
			return false
		}
	}
	if e.ShowAs == "free" {
		if !cfg.CountsFree() {
			return false
		}
	}
	if e.IsTentative() {
		if !cfg.CountsTentative() {
			return false
		}
	}
	if e.AllDay {
		if cfg.CountsAllDay() {
			// normal rules apply; free switch still excludes free
			if e.ShowAs == "free" && !cfg.CountsFree() {
				return false
			}
		} else {
			// counts only when marked busy
			if e.ShowAs != "busy" && e.ShowAs != "oof" {
				return false
			}
			if e.ShowAs == "free" {
				return false
			}
		}
	}
	return true
}

// LinkPositive reports whether any member of a link is positive.
func LinkPositive(l calendar.Link, bySource map[string]config.ConflictConfig) bool {
	for _, m := range l.Members {
		cfg := bySource[m.SourceID]
		if Positive(m, cfg) {
			return true
		}
	}
	return false
}

// Detect finds pairwise conflicts among links.
func Detect(links []calendar.Link, bySource map[string]config.ConflictConfig, dismissed map[string]time.Time) []Conflict {
	var out []Conflict
	for i := 0; i < len(links); i++ {
		for j := i + 1; j < len(links); j++ {
			a, b := links[i], links[j]
			ca, cb := a.Canonical, b.Canonical
			// overlap: a.Start < b.End && b.Start < a.End
			if !(ca.Start.Before(cb.End) && cb.Start.Before(ca.End)) {
				continue
			}
			// zero-duration never overlaps handled by strict inequalities above
			if !LinkPositive(a, bySource) || !LinkPositive(b, bySource) {
				continue
			}
			// linked rows never conflict (same link already excluded by i!=j; but check members overlap? rows linked as same meeting are in same link, so distinct links only)
			key := conflictKey(a, b)
			tent := ca.IsTentative() || cb.IsTentative()
			_, isDismissed := dismissed[key]
			out = append(out, Conflict{Key: key, A: a, B: b, Tentative: tent, Dismissed: isDismissed})
		}
	}
	return out
}

func conflictKey(a, b calendar.Link) string {
	ca, cb := a.Canonical, b.Canonical
	s1 := fmt.Sprintf("%s|%d|%d", a.Key, ca.Start.Unix(), ca.End.Unix())
	s2 := fmt.Sprintf("%s|%d|%d", b.Key, cb.Start.Unix(), cb.End.Unix())
	pair := []string{s1, s2}
	sort.Strings(pair)
	h := sha256.Sum256([]byte(strings.Join(pair, "||")))
	return hex.EncodeToString(h[:])[:16]
}

// Fetch wires store, config, calendar links.
func Fetch(ctx context.Context, from, to time.Time) ([]Conflict, error) {
	links, err := calendar.LinksInRange(ctx, from, to)
	if err != nil {
		return nil, err
	}
	uc := config.UserConfigFromContext(ctx)
	bySource := make(map[string]config.ConflictConfig)
	for i, s := range uc.Sources {
		_ = i
		bySource[s.ID] = s.Conflicts
	}
	dismissed, err := store.FromContext(ctx).ListDismissedConflicts(ctx)
	if err != nil {
		return nil, err
	}
	return Detect(links, bySource, dismissed), nil
}
