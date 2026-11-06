package caldav

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Kenttleton/focacal/internal/config"
	"github.com/Kenttleton/focacal/internal/connector"
	"github.com/Kenttleton/focacal/internal/store"
)

// TestWriteEventIDIsWhatSyncStores: the remote id WriteEvent returns must be
// the UID Sync stores, or the write ledger cannot recognize the echo.
func TestWriteEventIDIsWhatSyncStores(t *testing.T) {
	var mu sync.Mutex
	objects := map[string]string{} // path -> ics body
	var deleted []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodPut:
			objects[r.URL.Path] = string(body)
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodDelete:
			deleted = append(deleted, r.URL.Path)
			delete(objects, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == "REPORT":
			w.Header().Set("Content-Type", "application/xml; charset=utf-8")
			w.WriteHeader(http.StatusMultiStatus)
			var b strings.Builder
			b.WriteString(msHead)
			for p, ics := range objects {
				b.WriteString(`<D:response><D:href>` + p + `</D:href><D:propstat><D:prop><D:getetag>"1"</D:getetag><C:calendar-data><![CDATA[` + ics + `]]></C:calendar-data></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response>`)
			}
			b.WriteString(`</D:multistatus>`)
			io.WriteString(w, b.String())
		case strings.Contains(string(body), "current-user-principal"):
			w.Header().Set("Content-Type", "application/xml; charset=utf-8")
			w.WriteHeader(http.StatusMultiStatus)
			io.WriteString(w, msHead+propResp(r.URL.Path, `<D:current-user-principal><D:href>/principal/</D:href></D:current-user-principal>`)+`</D:multistatus>`)
		case strings.Contains(string(body), "calendar-home-set"):
			w.Header().Set("Content-Type", "application/xml; charset=utf-8")
			w.WriteHeader(http.StatusMultiStatus)
			io.WriteString(w, msHead+propResp(r.URL.Path, `<C:calendar-home-set><D:href>/home/</D:href></C:calendar-home-set>`)+`</D:multistatus>`)
		default:
			w.Header().Set("Content-Type", "application/xml; charset=utf-8")
			w.WriteHeader(http.StatusMultiStatus)
			io.WriteString(w, msHead+propResp("/home/a/", `<D:resourcetype><D:collection/><C:calendar/></D:resourcetype><C:supported-calendar-component-set><C:comp name="VEVENT"/></C:supported-calendar-component-set>`)+`</D:multistatus>`)
		}
	}))
	defer srv.Close()

	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := New(config.SourceConfig{ID: "work-a", Type: "caldav", CalDAVURL: srv.URL, CalendarIDs: []string{"a"}}, db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	start := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	end := start.Add(time.Hour)

	id, err := c.WriteEvent(ctx, &connector.RemoteEvent{Title: "Focus", Start: start, End: end, ManagedTag: true})
	if err != nil {
		t.Fatal(err)
	}
	// Update by the returned id keeps the same UID and object.
	id2, err := c.WriteEvent(ctx, &connector.RemoteEvent{RemoteID: id, Title: "Focus 2", Start: start, End: end})
	if err != nil || id2 != id {
		t.Fatalf("update returned %q (%v), want %q", id2, err, id)
	}
	if len(objects) != 1 {
		t.Fatalf("update created a second object: %v", objects)
	}

	if err := db.LedgerPut(ctx, store.LedgerEntry{CalendarID: "work-a", RemoteID: id, DestinationID: "d", ItemKey: "k",
		Hash: store.FieldHash("Focus 2", "", start, end)}); err != nil {
		t.Fatal(err)
	}
	if err := c.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	evs, _ := db.ListEvents(ctx, start.Add(-24*time.Hour), end.Add(24*time.Hour))
	if len(evs) != 0 {
		t.Fatalf("sync stored our own write: %+v", evs[0])
	}
	// Without the ledger the same object is stored with UID == the returned id.
	_ = db.LedgerDelete(ctx, "work-a", id)
	if err := c.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	evs, _ = db.ListEvents(ctx, start.Add(-24*time.Hour), end.Add(24*time.Hour))
	if len(evs) != 1 || evs[0].UID != id {
		t.Fatalf("stored UID mismatch: %+v want %q", evs, id)
	}

	if err := c.DeleteEvent(ctx, id); err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 1 || !strings.HasSuffix(deleted[0], ".ics") || len(objects) != 0 {
		t.Fatalf("delete went to %v", deleted)
	}
}
