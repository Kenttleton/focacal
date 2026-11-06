package caldav

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	configexample "github.com/Kenttleton/focacal/config.example"
	"github.com/Kenttleton/focacal/internal/config"
	"github.com/Kenttleton/focacal/internal/store"
)

const twoFeeds = `calendars:
  - id: work
    name: Work
    url: https://feed.test/work.ics
destinations:
  - id: focacal
    name: Focacal
    inputs: all
    target: {served: true}
  - id: busy
    name: Busy only
    inputs:
      - calendar: work
        event:
          title: {visibility: false}
          body: {visibility: false}
    target: {served: true}
  - id: pushed
    inputs: all
    target: {calendar: work-out}
`

func writeCal(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "calendars.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newTestServer returns a handler over a store holding one "work" event, and
// the live config in dir.
func newTestServer(t *testing.T, calendars string) (http.Handler, *config.Live, string) {
	t.Helper()
	dir := t.TempDir()
	writeCal(t, dir, calendars)
	live, err := config.NewLive(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	now := time.Now()
	if err := db.UpsertEvent(t.Context(), &store.Event{
		ID: "work:1", SourceID: "work", CalendarID: "c", UID: "1", Title: "Therapy",
		Description: "private notes", Start: now.Add(time.Hour), End: now.Add(2 * time.Hour),
		Status: "confirmed", SyncedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	s := New("127.0.0.1:0", live, nil)
	h := s.srv.Handler
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(store.WithStore(r.Context(), db)))
	}), live, dir
}

func do(h http.Handler, method, path string) (int, string) {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	b, _ := io.ReadAll(w.Result().Body)
	return w.Code, string(b)
}

func TestServedFeedsAreIndependent(t *testing.T) {
	h, _, _ := newTestServer(t, twoFeeds)

	code, full := do(h, "GET", "/calendar/focacal/")
	if code != 200 || !strings.Contains(full, "SUMMARY:Therapy") || !strings.Contains(full, "private notes") {
		t.Fatalf("full feed: %d\n%s", code, full)
	}
	code, busy := do(h, "GET", "/calendar/busy/")
	if code != 200 || !strings.Contains(busy, "SUMMARY:Busy") || strings.Contains(busy, "Therapy") || strings.Contains(busy, "private notes") {
		t.Fatalf("busy feed: %d\n%s", code, busy)
	}
	if !strings.Contains(busy, "X-WR-CALNAME:Busy only") {
		t.Errorf("feed name missing:\n%s", busy)
	}
	for _, p := range []string{"/calendar/nope/", "/calendar/pushed/"} {
		if code, _ := do(h, "GET", p); code != 404 {
			t.Errorf("%s = %d, want 404 (unknown or not served)", p, code)
		}
	}
	for _, p := range []string{"/focacal/", "/focacal/calendar/"} {
		if code, body := do(h, "GET", p); code != 200 || !strings.Contains(body, "Therapy") {
			t.Errorf("alias %s = %d", p, code)
		}
	}
	code, list := do(h, "PROPFIND", "/calendar/")
	if code != 207 || !strings.Contains(list, "Focacal") || !strings.Contains(list, "Busy only") || strings.Contains(list, "pushed") {
		t.Errorf("list: %d\n%s", code, list)
	}
	if code, list = do(h, "GET", "/calendar/"); code != 200 || !strings.Contains(list, "Busy only") {
		t.Errorf("GET list: %d\n%s", code, list)
	}
	if code, ct := do(h, "PROPFIND", "/calendar/busy/"); code != 207 || !strings.Contains(ct, "<displayname>Busy only</displayname>") || !strings.Contains(ct, "getctag") {
		t.Errorf("propfind feed: %d\n%s", code, ct)
	}
	if code, _ := do(h, "OPTIONS", "/calendar/busy/"); code != 200 {
		t.Errorf("options = %d", code)
	}
	if code, _ := do(h, "REPORT", "/calendar/busy/"); code != 200 {
		t.Errorf("report = %d", code)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/.well-known/caldav", nil))
	if w.Code != http.StatusMovedPermanently || w.Header().Get("Location") != "/calendar/" {
		t.Errorf("well-known: %d %s", w.Code, w.Header().Get("Location"))
	}
}

func TestServedAliasWithoutFocacalDestination(t *testing.T) {
	h, _, _ := newTestServer(t, "calendars:\n  - id: work\n    url: https://x.test/w.ics\ndestinations:\n  - id: other\n    inputs: all\n    target: {served: true}\n")
	if code, _ := do(h, "GET", "/focacal/"); code != 404 {
		t.Errorf("alias without focacal destination = %d, want 404", code)
	}
	if code, body := do(h, "PROPFIND", "/calendar/other/"); code != 207 || !strings.Contains(body, "<displayname>other</displayname>") {
		t.Errorf("name falls back to id: %d\n%s", code, body)
	}
}

func TestServedCtagTracksProjectedOutput(t *testing.T) {
	h, _, _ := newTestServer(t, twoFeeds)
	_, a := do(h, "PROPFIND", "/calendar/busy/")
	_, b := do(h, "PROPFIND", "/calendar/busy/")
	_, c := do(h, "PROPFIND", "/calendar/focacal/")
	tag := func(s string) string {
		i := strings.Index(s, "<CS:getctag>")
		if i < 0 {
			return ""
		}
		return s[i : i+strings.Index(s[i:], "</CS:getctag>")]
	}
	if tag(a) == "" || tag(a) != tag(b) || tag(a) == tag(c) {
		t.Errorf("ctags: busy=%q busy2=%q full=%q", tag(a), tag(b), tag(c))
	}
}

func TestServedDestinationAppliesWithoutRestart(t *testing.T) {
	h, live, dir := newTestServer(t, twoFeeds)
	if code, _ := do(h, "GET", "/calendar/later/"); code != 404 {
		t.Fatalf("before = %d", code)
	}
	writeCal(t, dir, twoFeeds+"  - id: later\n    name: Later\n    inputs: all\n    target: {served: true}\n")
	if _, err := live.Reload(); err != nil {
		t.Fatal(err)
	}
	if code, _ := do(h, "GET", "/calendar/later/"); code != 200 {
		t.Errorf("after reload = %d, want 200", code)
	}
}

// A first run on an empty directory serves the default focacal feed.
func TestFreshInstallServesDefaultFeed(t *testing.T) {
	dir := t.TempDir()
	if _, err := config.SeedDefaults(dir, configexample.FS); err != nil {
		t.Fatal(err)
	}
	live, err := config.NewLive(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var found bool
	for _, d := range live.Current().Destinations {
		found = found || (d.ID == "focacal" && d.Target.Served)
	}
	if !found {
		t.Fatal("no served focacal destination after seeding")
	}
	h := New("127.0.0.1:0", live, nil).srv.Handler
	r := httptest.NewRequest("GET", "/calendar/focacal/", nil).WithContext(store.WithStore(t.Context(), db))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "BEGIN:VCALENDAR") {
		t.Errorf("default feed: %d", w.Code)
	}
}
