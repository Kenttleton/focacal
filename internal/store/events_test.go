package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestDeleteEventsBySource(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	mk := func(src, uid string) *Event {
		return &Event{ID: EventID(src, "c", uid), SourceID: src, CalendarID: "c", UID: uid,
			Start: now, End: now.Add(time.Hour), Status: "confirmed"}
	}
	for _, e := range []*Event{mk("a", "1"), mk("a", "2"), mk("b", "1")} {
		if err := db.UpsertEvent(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	n, err := db.DeleteEventsBySource(ctx, "a")
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v, want 2", n, err)
	}
	evs, err := db.ListEvents(ctx, now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil || len(evs) != 1 || evs[0].SourceID != "b" {
		t.Fatalf("remaining = %v err=%v", evs, err)
	}
	if n, _ := db.DeleteEventsBySource(ctx, "zzz"); n != 0 {
		t.Fatalf("unknown source deleted %d", n)
	}
}
