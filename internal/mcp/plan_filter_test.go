package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/Kenttleton/focacal/internal/config"
	"github.com/Kenttleton/focacal/internal/store"
)

func planFilterCtx(t *testing.T) (context.Context, *store.DB) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	uc := &config.UserConfig{
		WorkingHours: config.WorkingHours{Start: "09:00", End: "17:00", Timezone: "UTC"},
		Focus:        config.FocusConfig{Style: "flexible", MinBlockMinutes: 30, MaxBlockMinutes: 600},
		Sources: []config.SourceConfig{
			{ID: "planned", Plan: true},
			{ID: "ignored", Plan: false},
		},
	}
	ctx := store.WithStore(config.WithUserConfig(context.Background(), uc), db)
	day := time.Date(2030, 1, 7, 0, 0, 0, 0, time.UTC)
	for _, e := range []struct {
		src  string
		hour int
	}{{"planned", 10}, {"ignored", 13}} {
		start := day.Add(time.Duration(e.hour) * time.Hour)
		ev := &store.Event{ID: store.EventID(e.src, "c", "u"), SourceID: e.src, CalendarID: "c", UID: "u",
			Title: e.src, Start: start, End: start.Add(time.Hour), Status: "confirmed"}
		if err := db.UpsertEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	return ctx, db
}

func TestMCPListEventsExcludesNonPlanCalendars(t *testing.T) {
	ctx, db := planFilterCtx(t)
	res, err := (&Server{}).toolListEvents(ctx, json.RawMessage(`{"from":"2030-01-07","to":"2030-01-08"}`))
	if err != nil {
		t.Fatal(err)
	}
	var got []struct{ SourceID string }
	if err := json.Unmarshal([]byte(res.Content[0].Text), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SourceID != "planned" {
		t.Fatalf("list_events = %+v, want only the plan calendar's event", got)
	}

	// computeGaps ignores the non-plan event: 09-10 (60) and 11-17 (360)
	// stay free apart from the plan event; 13:00 is not busy.
	gaps, err := computeGaps(ctx, time.Date(2030, 1, 7, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	busy := time.Date(2030, 1, 7, 13, 30, 0, 0, time.UTC)
	covered := false
	for _, g := range gaps {
		if g.Start.Before(busy) && g.End.After(busy) {
			covered = true
		}
	}
	if !covered || len(gaps) != 2 {
		t.Fatalf("gaps = %+v, want 09-10 and 11-17 with 13:30 free", gaps)
	}

	// The store (REST and UI read from it) still holds both events.
	all, err := db.ListEvents(ctx, time.Date(2030, 1, 7, 0, 0, 0, 0, time.UTC), time.Date(2030, 1, 8, 0, 0, 0, 0, time.UTC))
	if err != nil || len(all) != 2 {
		t.Fatalf("store events = %d, %v, want both", len(all), err)
	}
}
