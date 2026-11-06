package outlook

import "testing"

// WriteEvent returns the Graph event "id"; sync stores the same value as
// Event.UID, which is what the write ledger is keyed by.
func TestSyncedUIDIsTheWrittenEventID(t *testing.T) {
	e := toStoreEvent("work", "cal1", outlookEvent{ID: "AAMk=", Subject: "Focus"})
	if e.UID != "AAMk=" || e.SourceID != "work" {
		t.Fatalf("UID=%q SourceID=%q, want the Graph id and the calendar id", e.UID, e.SourceID)
	}
}
