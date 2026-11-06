package caldav

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/Kenttleton/focacal/internal/config"
	"github.com/Kenttleton/focacal/internal/store"
)

const msHead = `<?xml version="1.0" encoding="utf-8"?><D:multistatus xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">`

func propResp(href, props string) string {
	return fmt.Sprintf(`<D:response><D:href>%s</D:href><D:propstat><D:prop>%s</D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response>`, href, props)
}

// fakeCalDAV serves a principal, a home set with the given collections and
// records the collection paths that receive a calendar-query REPORT.
func fakeCalDAV(t *testing.T, collections ...string) (url string, queried func() []string) {
	t.Helper()
	var mu sync.Mutex
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.WriteHeader(http.StatusMultiStatus)
		switch {
		case r.Method == "REPORT":
			mu.Lock()
			got = append(got, r.URL.Path)
			mu.Unlock()
			io.WriteString(w, msHead+`</D:multistatus>`)
		case strings.Contains(string(body), "current-user-principal"):
			io.WriteString(w, msHead+propResp(r.URL.Path, `<D:current-user-principal><D:href>/principal/</D:href></D:current-user-principal>`)+`</D:multistatus>`)
		case strings.Contains(string(body), "calendar-home-set"):
			io.WriteString(w, msHead+propResp(r.URL.Path, `<C:calendar-home-set><D:href>/home/</D:href></C:calendar-home-set>`)+`</D:multistatus>`)
		default: // FindCalendars
			var b strings.Builder
			b.WriteString(msHead)
			for _, c := range collections {
				b.WriteString(propResp(c, `<D:resourcetype><D:collection/><C:calendar/></D:resourcetype><C:supported-calendar-component-set><C:comp name="VEVENT"/></C:supported-calendar-component-set>`))
			}
			b.WriteString(`</D:multistatus>`)
			io.WriteString(w, b.String())
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL, func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := append([]string(nil), got...)
		sort.Strings(out)
		return out
	}
}

func syncWith(t *testing.T, calendarIDs []string) []string {
	t.Helper()
	url, queried := fakeCalDAV(t, "/home/a/", "/home/b/")
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := New(config.SourceConfig{ID: "work-a", AccountID: "work", Type: "caldav", CalDAVURL: url, CalendarIDs: calendarIDs}, db)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	return queried()
}

func TestCalDAVSyncFiltersByRemoteID(t *testing.T) {
	if got := syncWith(t, []string{"/home/a/"}); !reflect.DeepEqual(got, []string{"/home/a/"}) {
		t.Fatalf("with remote id: synced %v, want only /home/a/", got)
	}
	if got := syncWith(t, nil); !reflect.DeepEqual(got, []string{"/home/a/", "/home/b/"}) {
		t.Fatalf("without remote id: synced %v, want every collection", got)
	}
}

// The remote id names one collection exactly: a path that merely contains it
// (/home/a/ inside /home/a/archive/) must not match.
func TestCalDAVRemoteIDMatchesWholePath(t *testing.T) {
	c := &Connector{cfg: config.SourceConfig{CalendarIDs: []string{"/home/a/"}}}
	for path, want := range map[string]bool{
		"/home/a/":         true,
		"/home/a":          true, // trailing slash is not significant
		"/home/a/archive/": false,
		"/home/ab/":        false,
		"/x/home/a/":       false,
	} {
		if got := c.wantCalendar("/home/", path); got != want {
			t.Errorf("wantCalendar(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestCalDAVRemoteIDForms(t *testing.T) {
	for _, id := range []string{"a", "A", "/HOME/a", "http://dav.example/home/a/", "/home/%61/"} {
		if got := syncWith(t, []string{id}); !reflect.DeepEqual(got, []string{"/home/a/"}) {
			t.Errorf("remote id %q: synced %v, want only /home/a/", id, got)
		}
	}
	if got := syncWith(t, []string{"archive"}); len(got) != 0 {
		t.Errorf("fragment under another collection synced %v", got)
	}
}

func TestCalDAVNoMatchWarnsByCalendarID(t *testing.T) {
	var buf strings.Builder
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(old)
	if got := syncWith(t, []string{"/secret-remote/"}); len(got) != 0 {
		t.Fatalf("synced %v, want nothing", got)
	}
	out := buf.String()
	if !strings.Contains(out, "work-a") || strings.Contains(out, "secret-remote") {
		t.Fatalf("log = %q, want the calendar id and not the remote id", out)
	}
}
