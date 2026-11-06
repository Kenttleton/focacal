package google

import "testing"

// WriteEvent returns the API's event "id"; sync stores the same value as
// Event.UID and uses it in the composite id, which is what the write ledger
// is keyed by.
func TestSyncedUIDIsTheWrittenEventID(t *testing.T) {
	e := toStoreEvent("work", "primary", googleEvent{ID: "abc123", ICalUID: "abc123@google.com", Summary: "Focus"})
	if e.UID != "abc123" || e.SourceID != "work" {
		t.Fatalf("UID=%q SourceID=%q, want the API id and the calendar id", e.UID, e.SourceID)
	}
}
