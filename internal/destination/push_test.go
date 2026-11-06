package destination

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Kenttleton/focacal/internal/config"
	"github.com/Kenttleton/focacal/internal/connector"
	"github.com/Kenttleton/focacal/internal/store"
)

// fakeConn is a calendar: WriteEvent/DeleteEvent edit an in-memory remote and
// record calls; sync copies the remote into the store the way a real
// connector does (UID = the id WriteEvent returned).
type fakeConn struct {
	id     string
	mu     sync.Mutex
	remote map[string]connector.RemoteEvent
	writes []connector.RemoteEvent
	dels   []string
	failW  bool
	seq    int
}

func newFake(id string) *fakeConn {
	return &fakeConn{id: id, remote: map[string]connector.RemoteEvent{}}
}

func (f *fakeConn) SourceID() string                              { return f.id }
func (f *fakeConn) Sync(context.Context) error                    { return nil }
func (f *fakeConn) RegisterWebhook(context.Context, string) error { return nil }
func (f *fakeConn) ValidateWebhook(context.Context, []byte, map[string]string) (string, error) {
	return f.id, nil
}
func (f *fakeConn) WriteEvent(_ context.Context, e *connector.RemoteEvent) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failW {
		return "", errors.New("boom https://secret.test/?token=hunter2")
	}
	f.writes = append(f.writes, *e)
	id := e.RemoteID
	if id == "" {
		f.seq++
		id = fmt.Sprintf("%s-r%d", f.id, f.seq)
	}
	f.remote[id] = *e
	return id, nil
}
func (f *fakeConn) DeleteEvent(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dels = append(f.dels, id)
	delete(f.remote, id)
	return nil
}

// echo is a sync of this calendar: every remote event is ingested.
func (f *fakeConn) echo(t *testing.T, db *store.DB) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, r := range f.remote {
		s, _ := r.Start.(time.Time)
		e, _ := r.End.(time.Time)
		if err := db.UpsertEvent(context.Background(), &store.Event{
			ID: store.EventID(f.id, "primary", id), SourceID: f.id, CalendarID: "primary", UID: id,
			Title: r.Title, Description: r.Description, Start: s, End: e, Status: "confirmed", ShowAs: "busy",
		}); err != nil {
			t.Fatal(err)
		}
	}
}

type env struct {
	db    *store.DB
	live  *config.Live
	conns map[string]*fakeConn
	p     *Pusher
	logs  *bytes.Buffer
	now   time.Time
}

func newEnv(t *testing.T, yamlText string, calIDs ...string) *env {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "calendars.yaml"), []byte(yamlText), 0o644); err != nil {
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
	t.Cleanup(func() { db.Close() })
	e := &env{db: db, live: live, conns: map[string]*fakeConn{}, logs: &bytes.Buffer{}, now: time.Now().Truncate(time.Second)}
	for _, id := range calIDs {
		e.conns[id] = newFake(id)
	}
	lookup := func(id string) (connector.Connector, bool) {
		c, ok := e.conns[id]
		if !ok {
			return nil, false
		}
		return c, true
	}
	e.p = NewPusher(live, lookup, nil, db, slog.New(slog.NewTextHandler(e.logs, nil)))
	e.p.now = func() time.Time { return e.now }
	return e
}

func (e *env) event(t *testing.T, src, uid, title string, at time.Duration) {
	t.Helper()
	s := e.now.Add(at)
	if err := e.db.UpsertEvent(context.Background(), &store.Event{ID: store.EventID(src, "primary", uid), SourceID: src,
		CalendarID: "primary", UID: uid, Title: title, Description: "notes", Start: s, End: s.Add(time.Hour), Status: "confirmed"}); err != nil {
		t.Fatal(err)
	}
}

func (e *env) block(t *testing.T, id, title string, at time.Duration) *store.Block {
	t.Helper()
	s := e.now.Add(at)
	b := store.NewBlock(store.BlockFocus, title, s, s.Add(time.Hour), 1)
	b.ID = id
	if err := e.db.UpsertBlock(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	return b
}

const baseCfg = `accounts:
  - id: acct
    type: caldav
    caldav_url: https://cal.test
calendars:
  - id: work
    account: acct
    remote_id: work
  - id: third
    account: acct
    remote_id: third
  - id: personal
    account: acct
    remote_id: personal
  - id: feed
    url: https://feed.test/x.ics
destinations:
`

func TestPusherWritesAndUpdates(t *testing.T) {
	e := newEnv(t, baseCfg+`  - id: out
    inputs:
      - calendar: focacal
    target: {calendar: work}
`, "work")
	ctx := context.Background()
	e.block(t, "b1", "Focus", 2*time.Hour)
	e.block(t, "b2", "Focus 2", 5*time.Hour)

	if err := e.p.Notify(ctx); err != nil {
		t.Fatal(err)
	}
	w := e.conns["work"]
	if len(w.writes) != 2 {
		t.Fatalf("writes = %d, want 2", len(w.writes))
	}
	for _, r := range w.writes {
		if !r.ManagedTag || r.ShowAs != "busy" || r.RemoteID != "" {
			t.Errorf("bad create: %+v", r)
		}
	}
	// Unchanged: nothing written.
	if err := e.p.Notify(ctx); err != nil {
		t.Fatal(err)
	}
	if len(w.writes) != 2 || len(w.dels) != 0 {
		t.Fatalf("second notify wrote %d, deleted %d", len(w.writes)-2, len(w.dels))
	}
	// Changed block updates by the stored remote id.
	led, _ := e.db.LedgerByDestination(ctx, "out")
	var rid string
	for _, l := range led {
		if l.ItemKey == "b1" {
			rid = l.RemoteID
		}
	}
	e.block(t, "b1", "Deep focus", 2*time.Hour)
	if err := e.p.Notify(ctx); err != nil {
		t.Fatal(err)
	}
	if len(w.writes) != 3 || w.writes[2].RemoteID != rid || w.writes[2].Title != "Deep focus" {
		t.Fatalf("update: %+v", w.writes)
	}
	// Removed block deletes remotely and from the ledger.
	if err := e.db.DeleteBlock(ctx, "b2"); err != nil {
		t.Fatal(err)
	}
	if err := e.p.Notify(ctx); err != nil {
		t.Fatal(err)
	}
	if len(w.dels) != 1 || len(w.remote) != 1 {
		t.Fatalf("dels=%v remote=%d", w.dels, len(w.remote))
	}
	if led, _ := e.db.LedgerByDestination(ctx, "out"); len(led) != 1 {
		t.Fatalf("ledger = %v", led)
	}
}

func TestPusherMirrorsEventsVisibilityAndSkipsAllDay(t *testing.T) {
	e := newEnv(t, baseCfg+`  - id: out
    inputs:
      - calendar: personal
        event:
          title: {visibility: false, rename: Busy}
          body: {visibility: false}
    target: {calendar: work}
`, "work", "personal")
	ctx := context.Background()
	e.event(t, "personal", "u1", "Therapy", time.Hour)
	s := e.now.Add(3 * time.Hour)
	_ = e.db.UpsertEvent(ctx, &store.Event{ID: "personal:primary:ooo", SourceID: "personal", UID: "ooo", Title: "OOO",
		Start: s, End: s.Add(24 * time.Hour), AllDay: true, Status: "confirmed"})
	if err := e.p.Notify(ctx); err != nil {
		t.Fatal(err)
	}
	w := e.conns["work"]
	if len(w.writes) != 1 {
		t.Fatalf("writes = %+v", w.writes)
	}
	r := w.writes[0]
	if r.Title != "Busy" || r.Description != "" || r.ShowAs != "busy" || !r.ManagedTag {
		t.Errorf("mirror = %+v", r)
	}
}

func TestPusherIsolatesFailures(t *testing.T) {
	e := newEnv(t, baseCfg+`  - id: failing
    inputs: [{calendar: focacal}]
    target: {calendar: work}
  - id: missing
    inputs: [{calendar: focacal}]
    target: {calendar: personal}
  - id: good
    inputs: [{calendar: focacal}]
    target: {calendar: third}
`, "work", "third")
	// "personal" has no connector (it failed to build).
	e.conns["work"].failW = true
	e.block(t, "b1", "Focus", time.Hour)
	err := e.p.Notify(context.Background())
	if err == nil {
		t.Fatal("want an error naming the failed destinations")
	}
	if len(e.conns["third"].writes) != 1 {
		t.Fatal("a failing destination stopped the others")
	}
	logs := e.logs.String() + err.Error()
	for _, id := range []string{"destination=failing", "destination=missing"} {
		if !strings.Contains(logs, id) {
			t.Errorf("%s not logged: %s", id, logs)
		}
	}
	if strings.Contains(logs, "destination=good") {
		t.Errorf("healthy destination logged as failed: %s", logs)
	}
	for _, leak := range []string{"secret.test", "hunter2", "boom"} {
		if strings.Contains(logs, leak) {
			t.Errorf("log leaks %q: %s", leak, logs)
		}
	}
	if led, _ := e.db.LedgerByDestination(context.Background(), "failing"); len(led) != 0 {
		t.Errorf("failed write recorded in the ledger: %v", led)
	}
}

func TestWritableTargetRejectsURLOnly(t *testing.T) {
	cfg := &config.Config{}
	cfg.Accounts = []config.AccountConfig{{ID: "acct", Type: "caldav", CalDAVURL: "https://cal.test"}}
	cfg.Calendars = []config.CalendarConfig{
		{ID: "feed", URL: "https://feed.test/x.ics"},
		{ID: "work", Account: "acct"},
	}
	cfg.Resolve()
	if writableTarget(cfg, "feed") || writableTarget(cfg, "nope") || !writableTarget(cfg, "work") {
		t.Fatal("writableTarget wrong")
	}
}

func TestPusherMissingTargetDoesNotStopOthers(t *testing.T) {
	e := newEnv(t, baseCfg+`  - id: first
    inputs: [{calendar: focacal}]
    target: {calendar: personal}
  - id: second
    inputs: [{calendar: focacal}]
    target: {calendar: work}
`, "work")
	e.block(t, "b1", "Focus", time.Hour)
	if err := e.p.Notify(context.Background()); err == nil {
		t.Fatal("want error")
	}
	if len(e.conns["work"].writes) != 1 {
		t.Fatal("second destination was skipped")
	}
	if !strings.Contains(e.logs.String(), "destination=first") {
		t.Errorf("logs: %s", e.logs.String())
	}
}

// Spec section 7: a plan:true target that also has the focacal input must not
// read the focus blocks it received back as meetings.
func TestPlanTrueTargetDoesNotReadBackFocusBlocks(t *testing.T) {
	e := newEnv(t, baseCfg+`  - id: out
    inputs: [{calendar: focacal}]
    target: {calendar: work}
`, "work")
	ctx := context.Background()
	e.block(t, "b1", "Focus", 2*time.Hour)
	if err := e.p.Notify(ctx); err != nil {
		t.Fatal(err)
	}
	e.conns["work"].echo(t, e.db) // the next sync returns what we wrote
	evs, _ := e.db.ListEvents(ctx, e.now.Add(-24*time.Hour), e.now.Add(48*time.Hour))
	if len(evs) != 0 {
		t.Fatalf("own focus block read back as an event: %+v", evs)
	}
	// A real edit on the remote is still ingested.
	w := e.conns["work"]
	for id, r := range w.remote {
		r.Title = "Moved by user"
		w.remote[id] = r
	}
	w.echo(t, e.db)
	if evs, _ := e.db.ListEvents(ctx, e.now.Add(-24*time.Hour), e.now.Add(48*time.Hour)); len(evs) != 1 {
		t.Fatalf("real edit not ingested: %+v", evs)
	}
}

// A to B and B to C settle after one round: the echo at each hop is ignored.
func TestCrossDestinationChainNoLoop(t *testing.T) {
	e := newEnv(t, baseCfg+`  - id: a-to-b
    inputs: [{calendar: feed}]
    target: {calendar: work}
  - id: b-to-c
    inputs: [{calendar: work}]
    target: {calendar: personal}
`, "work", "personal")
	ctx := context.Background()
	e.event(t, "feed", "m1", "Standup", time.Hour)
	for round := 0; round < 3; round++ {
		if err := e.p.Notify(ctx); err != nil {
			t.Fatal(err)
		}
		e.conns["work"].echo(t, e.db)
		e.conns["personal"].echo(t, e.db)
	}
	if n := len(e.conns["work"].writes); n != 1 {
		t.Errorf("work writes = %d, want 1", n)
	}
	if n := len(e.conns["personal"].writes); n != 0 {
		t.Errorf("personal writes = %d, want 0 (work holds only our own echo)", n)
	}
	evs, _ := e.db.ListEvents(ctx, e.now.Add(-24*time.Hour), e.now.Add(48*time.Hour))
	if len(evs) != 1 || evs[0].SourceID != "feed" {
		t.Errorf("stored events: %+v", evs)
	}
}

func TestPusherSerializesPerDestination(t *testing.T) {
	e := newEnv(t, baseCfg+`  - id: out
    inputs: [{calendar: focacal}]
    target: {calendar: work}
`, "work")
	e.block(t, "b1", "Focus", time.Hour)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = e.p.Notify(context.Background()) }()
	}
	wg.Wait()
	if n := len(e.conns["work"].writes); n != 1 {
		t.Fatalf("concurrent notifies wrote %d times, want 1", n)
	}
}
