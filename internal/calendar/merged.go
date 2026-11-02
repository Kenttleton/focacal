package calendar

import (
	"context"
	"time"

	"github.com/Kenttleton/focacal/internal/config"
	"github.com/Kenttleton/focacal/internal/store"
)

// MergedEvents returns canonical rows in [from, to), sorted by start.
func MergedEvents(ctx context.Context, from, to time.Time) ([]*store.Event, error) {
	st := store.FromContext(ctx)
	uc := config.UserConfigFromContext(ctx)
	evs, err := st.ListEvents(ctx, from, to)
	if err != nil {
		return nil, err
	}
	order := SourceOrder(uc)
	links := Group(evs, order)
	out := make([]*store.Event, 0, len(links))
	for _, l := range links {
		out = append(out, l.Canonical)
	}
	sortEvents(out)
	return out, nil
}

// LinksInRange returns links in [from, to).
func LinksInRange(ctx context.Context, from, to time.Time) ([]Link, error) {
	st := store.FromContext(ctx)
	uc := config.UserConfigFromContext(ctx)
	evs, err := st.ListEvents(ctx, from, to)
	if err != nil {
		return nil, err
	}
	order := SourceOrder(uc)
	return Group(evs, order), nil
}

func sortEvents(evs []*store.Event) {
	// simple insertion sort by start
	for i := 1; i < len(evs); i++ {
		j := i
		for j > 0 && evs[j-1].Start.After(evs[j].Start) {
			evs[j-1], evs[j] = evs[j], evs[j-1]
			j--
		}
	}
}
