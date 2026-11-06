package outlook

import (
	"errors"
	"testing"

	"github.com/Kenttleton/focacal/internal/connector"
)

// WriteEvent returns the Graph event "id"; sync stores the same value as
// Event.UID, which is what the write ledger is keyed by.
func TestSyncedUIDIsTheWrittenEventID(t *testing.T) {
	e := toStoreEvent("work", "cal1", outlookEvent{ID: "AAMk=", Subject: "Focus"})
	if e.UID != "AAMk=" || e.SourceID != "work" {
		t.Fatalf("UID=%q SourceID=%q, want the Graph id and the calendar id", e.UID, e.SourceID)
	}
}

func TestNotFoundOnUpdate(t *testing.T) {
	if !errors.Is(notFoundOnUpdate(404, true), connector.ErrNotFound) {
		t.Error("update 404 must wrap ErrNotFound")
	}
	if notFoundOnUpdate(404, false) != nil || notFoundOnUpdate(500, true) != nil {
		t.Error("only an update 404 maps")
	}
}
