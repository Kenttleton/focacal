package google

import (
	"errors"
	"testing"

	"github.com/Kenttleton/focacal/internal/connector"
)

// WriteEvent returns the API's event "id"; sync stores the same value as
// Event.UID and uses it in the composite id, which is what the write ledger
// is keyed by.
func TestSyncedUIDIsTheWrittenEventID(t *testing.T) {
	e := toStoreEvent("work", "primary", googleEvent{ID: "abc123", ICalUID: "abc123@google.com", Summary: "Focus"})
	if e.UID != "abc123" || e.SourceID != "work" {
		t.Fatalf("UID=%q SourceID=%q, want the API id and the calendar id", e.UID, e.SourceID)
	}
}

func TestWriteErrorMapsNotFoundOnUpdate(t *testing.T) {
	if !errors.Is(writeError(404, true, nil), connector.ErrNotFound) {
		t.Error("update 404 must wrap ErrNotFound")
	}
	if errors.Is(writeError(404, false, nil), connector.ErrNotFound) {
		t.Error("create 404 must not map to ErrNotFound")
	}
	if writeError(200, true, nil) != nil || writeError(500, true, nil) == nil {
		t.Error("success/failure mapping wrong")
	}
}
