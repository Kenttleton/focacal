package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func openLedgerDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestFieldHashNormalizes(t *testing.T) {
	s := time.Date(2026, 11, 9, 14, 0, 0, 0, time.UTC)
	e := s.Add(time.Hour)
	base := FieldHash("Focus time", "Deep work", s, e)

	est := time.FixedZone("EST", -5*3600)
	for name, got := range map[string]string{
		"whitespace":  FieldHash("  Focus   time \n", "Deep work", s, e),
		"html body":   FieldHash("Focus time", "<div>Deep&nbsp;<b>work</b></div>", s, e),
		"timezone":    FieldHash("Focus time", "Deep work", s.In(est), e.In(est)),
		"body spaces": FieldHash("Focus time", "  Deep\n work ", s, e),
	} {
		if got != base {
			t.Errorf("%s: hash differs", name)
		}
	}
	for name, got := range map[string]string{
		"title": FieldHash("Focus times", "Deep work", s, e),
		"body":  FieldHash("Focus time", "Deeper work", s, e),
		"start": FieldHash("Focus time", "Deep work", s.Add(time.Minute), e),
		"end":   FieldHash("Focus time", "Deep work", s, e.Add(time.Minute)),
	} {
		if got == base {
			t.Errorf("%s change did not change the hash", name)
		}
	}
}

func TestLedgerCRUD(t *testing.T) {
	ctx := context.Background()
	db := openLedgerDB(t)
	if _, ok, _ := db.LedgerGet(ctx, "cal", "r1"); ok {
		t.Fatal("empty ledger reported a hit")
	}
	for _, e := range []LedgerEntry{
		{CalendarID: "cal", RemoteID: "r1", DestinationID: "d1", ItemKey: "k1", Hash: "h1"},
		{CalendarID: "cal", RemoteID: "r2", DestinationID: "d1", ItemKey: "k2", Hash: "h2"},
		{CalendarID: "other", RemoteID: "r1", DestinationID: "d2", ItemKey: "k1", Hash: "h3"},
	} {
		if err := db.LedgerPut(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	if h, ok, _ := db.LedgerGet(ctx, "cal", "r1"); !ok || h != "h1" {
		t.Fatalf("got %q %v", h, ok)
	}
	// Same item rewritten under a new remote id replaces the old row.
	if err := db.LedgerPut(ctx, LedgerEntry{CalendarID: "cal", RemoteID: "r9", DestinationID: "d1", ItemKey: "k1", Hash: "h9"}); err != nil {
		t.Fatal(err)
	}
	got, err := db.LedgerByDestination(ctx, "d1")
	if err != nil || len(got) != 2 {
		t.Fatalf("by destination: %v %v", got, err)
	}
	if _, ok, _ := db.LedgerGet(ctx, "cal", "r1"); ok {
		t.Error("replaced row still present")
	}
	if err := db.LedgerDelete(ctx, "cal", "r2"); err != nil {
		t.Fatal(err)
	}
	if err := db.LedgerDeleteByCalendar(ctx, "cal"); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.LedgerByDestination(ctx, "d1"); len(got) != 0 {
		t.Errorf("calendar purge left %v", got)
	}
	if _, ok, _ := db.LedgerGet(ctx, "other", "r1"); !ok {
		t.Error("purge removed another calendar's row")
	}
}

func TestIngestSkipsOwnWrite(t *testing.T) {
	ctx := context.Background()
	db := openLedgerDB(t)
	s := time.Date(2026, 11, 9, 14, 0, 0, 0, time.UTC)
	e := s.Add(time.Hour)
	if err := db.LedgerPut(ctx, LedgerEntry{CalendarID: "work", RemoteID: "abc", DestinationID: "d", ItemKey: "b1",
		Hash: FieldHash("Focus", "", s, e)}); err != nil {
		t.Fatal(err)
	}
	mk := func(title string) *Event {
		return &Event{ID: EventID("work", "primary", "abc"), SourceID: "work", CalendarID: "primary", UID: "abc",
			Title: title, Start: s, End: e, Status: "confirmed"}
	}
	list := func() []*Event {
		evs, err := db.ListEvents(ctx, s.Add(-time.Hour), e.Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		return evs
	}
	// Our own write, with remote rewriting whitespace and zone.
	own := mk(" Focus ")
	own.Start, own.End = s.In(time.FixedZone("X", 3600)), e.In(time.FixedZone("X", 3600))
	if err := db.UpsertEvent(ctx, own); err != nil {
		t.Fatal(err)
	}
	if len(list()) != 0 {
		t.Fatal("own write was stored")
	}
	// A user edit differs and is stored.
	if err := db.UpsertEvent(ctx, mk("Focus (moved by me)")); err != nil {
		t.Fatal(err)
	}
	if len(list()) != 1 {
		t.Fatal("user edit was not stored")
	}
	// The edit reverting to our content removes the stored row.
	if err := db.UpsertEvent(ctx, mk("Focus")); err != nil {
		t.Fatal(err)
	}
	if len(list()) != 0 {
		t.Fatal("matching event did not remove the stored row")
	}
	// A different calendar's same uid is unaffected.
	other := mk("Focus")
	other.SourceID, other.ID = "personal", EventID("personal", "primary", "abc")
	if err := db.UpsertEvent(ctx, other); err != nil {
		t.Fatal(err)
	}
	if len(list()) != 1 {
		t.Fatal("ledger leaked across calendars")
	}
}
