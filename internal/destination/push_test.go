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

	updateGone bool   // updates fail with ErrNotFound (remote event deleted)
	afterWrite func() // runs after a successful write, before it returns
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
	if e.RemoteID != "" && f.updateGone {
		delete(f.remote, e.RemoteID)
		return "", fmt.Errorf("gone: %w", connector.ErrNotFound)
	}
	f.writes = append(f.writes, *e)
	id := e.RemoteID
	if id == "" {
		f.seq++
		id = fmt.Sprintf("%s-r%d", f.id, f.seq)
	}
	f.remote[id] = *e
	if f.afterWrite != nil {
		hook := f.afterWrite
		f.afterWrite = nil
		f.mu.Unlock()
		hook()
		f.mu.Lock()
	}
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
	dir   string
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
	e := &env{dir: dir, db: db, live: live, conns: map[string]*fakeConn{}, logs: &bytes.Buffer{}, now: time.Now().Truncate(time.Second)}
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
	b1 := e.block(t, "b1", "Focus", 2*time.Hour)
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
		if l.ItemKey == blockKey(b1) {
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

func (e *env) rewriteConfig(t *testing.T, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(e.dir, "calendars.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := e.live.Reload(); err != nil {
		t.Fatal(err)
	}
}

func (e *env) count(t *testing.T) int {
	t.Helper()
	l, _ := e.db.LedgerByDestination(context.Background(), "out")
	return len(l)
}

const outFocacal = `  - id: out
    inputs: [{calendar: focacal}]
    target: {calendar: work}
`

// Ruling 13: history stays on the remote and keeps suppressing its echo.
func TestPusherKeepsAgedOutHistory(t *testing.T) {
	e := newEnv(t, baseCfg+outFocacal, "work")
	ctx := context.Background()
	e.block(t, "b1", "Focus", 2*time.Hour)
	if err := e.p.Notify(ctx); err != nil {
		t.Fatal(err)
	}
	w := e.conns["work"]
	e.now = e.now.Add(8 * 24 * time.Hour) // the block is now before the window
	if err := e.p.Notify(ctx); err != nil {
		t.Fatal(err)
	}
	if len(w.dels) != 0 || len(w.remote) != 1 || e.count(t) != 1 {
		t.Fatalf("history deleted: dels=%v remote=%d ledger=%d", w.dels, len(w.remote), e.count(t))
	}
	w.echo(t, e.db)
	if evs, _ := e.db.ListEvents(ctx, e.now.Add(-30*24*time.Hour), e.now); len(evs) != 0 {
		t.Fatalf("history echo not suppressed: %+v", evs)
	}
	e.now = e.now.Add(26 * 24 * time.Hour) // more than 30 days after it ended
	if err := e.p.Notify(ctx); err != nil {
		t.Fatal(err)
	}
	if len(w.dels) != 0 || len(w.remote) != 1 {
		t.Fatalf("forgetting must not delete remotely: dels=%v", w.dels)
	}
	if e.count(t) != 0 {
		t.Fatal("ledger row not forgotten after 30 days")
	}
}

func TestPusherMassDeleteGuard(t *testing.T) {
	e := newEnv(t, baseCfg+outFocacal, "work")
	ctx := context.Background()
	for i := 0; i < 8; i++ {
		e.block(t, fmt.Sprintf("b%d", i), "Focus", time.Duration(i+1)*2*time.Hour)
	}
	if err := e.p.Notify(ctx); err != nil {
		t.Fatal(err)
	}
	w := e.conns["work"]
	// Under the cap: removing 3 of 8 proceeds.
	for i := 0; i < 3; i++ {
		_ = e.db.DeleteBlock(ctx, fmt.Sprintf("b%d", i))
	}
	if err := e.p.Notify(ctx); err != nil {
		t.Fatal(err)
	}
	if len(w.dels) != 3 {
		t.Fatalf("small removal: dels=%d, want 3", len(w.dels))
	}
	// Everything gone: 5 of 5 is not over 5 removals; add one more block first.
	e.block(t, "extra", "Focus", 20*time.Hour)
	if err := e.p.Notify(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 3; i < 8; i++ {
		_ = e.db.DeleteBlock(ctx, fmt.Sprintf("b%d", i))
	}
	_ = e.db.DeleteBlock(ctx, "extra")
	// 6 removals, all of the ledger: guarded, in this pusher and in a fresh
	// one (first push after a process start counts as unchanged).
	before := len(w.dels)
	for _, p := range []*Pusher{e.p, NewPusher(e.live, e.p.lookup, nil, e.db, e.p.log)} {
		p.now = e.p.now
		if err := p.Notify(ctx); err == nil {
			t.Fatal("a guarded push should report failure")
		}
		if len(w.dels) != before || e.count(t) != 6 {
			t.Fatalf("mass delete not blocked: dels=%d ledger=%d", len(w.dels)-before, e.count(t))
		}
	}
	if !strings.Contains(e.logs.String(), "destination=out") {
		t.Errorf("guard not logged by id: %s", e.logs.String())
	}
	// A config change since the previous push allows the same deletes.
	e.rewriteConfig(t, baseCfg+`  - id: out
    inputs: [{calendar: feed}]
    target: {calendar: work}
`)
	if err := e.p.Notify(ctx); err != nil {
		t.Fatal(err)
	}
	if len(w.dels) != before+6 || e.count(t) != 0 {
		t.Fatalf("config change should allow deletes: dels=%d ledger=%d", len(w.dels)-before, e.count(t))
	}
}

func TestPusherRecreatesAfterRemoteDeletion(t *testing.T) {
	e := newEnv(t, baseCfg+outFocacal, "work")
	ctx := context.Background()
	e.block(t, "b1", "Focus", time.Hour)
	if err := e.p.Notify(ctx); err != nil {
		t.Fatal(err)
	}
	w := e.conns["work"]
	old, _ := e.db.LedgerByDestination(ctx, "out")
	w.updateGone = true
	e.block(t, "b1", "Focus v2", time.Hour)
	if err := e.p.Notify(ctx); err != nil {
		t.Fatal(err)
	}
	led, _ := e.db.LedgerByDestination(ctx, "out")
	last := w.writes[len(w.writes)-1]
	if len(led) != 1 || led[0].RemoteID == old[0].RemoteID || last.RemoteID != "" || last.Title != "Focus v2" {
		t.Fatalf("not recreated: ledger=%+v last=%+v", led, last)
	}
}

// Ruling 16: a push that waited for another must write current data.
func TestPusherReadsDataUnderTheLock(t *testing.T) {
	e := newEnv(t, baseCfg+outFocacal, "work")
	ctx := context.Background()
	e.block(t, "b1", "old", time.Hour)
	w := e.conns["work"]
	entered, release := make(chan struct{}), make(chan struct{})
	w.afterWrite = func() { close(entered); <-release }
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = e.p.Notify(ctx) }()
	<-entered
	go func() { defer wg.Done(); _ = e.p.Notify(ctx) }()
	time.Sleep(150 * time.Millisecond) // the second Notify is now waiting for the first
	e.block(t, "b1", "new", time.Hour)
	close(release)
	wg.Wait()
	led, _ := e.db.LedgerByDestination(ctx, "out")
	if len(led) != 1 || len(w.remote) != 1 {
		t.Fatalf("ledger=%d remote=%d", len(led), len(w.remote))
	}
	for _, r := range w.remote {
		if r.Title != "new" {
			t.Fatalf("remote holds stale %q", r.Title)
		}
	}
	b := e.block(t, "tmp", "new", time.Hour)
	_ = e.db.DeleteBlock(ctx, "tmp")
	if led[0].Hash != store.FieldHash("new", "", b.Start, b.End) {
		t.Fatal("ledger hash is not the newer data's")
	}
}

// Ruling 17: an expired parent context must not leave an untracked write.
func TestLedgerWrittenAfterParentContextCancelled(t *testing.T) {
	e := newEnv(t, baseCfg+outFocacal, "work")
	ctx, cancel := context.WithCancel(context.Background())
	e.conns["work"].afterWrite = cancel
	e.block(t, "b1", "Focus", time.Hour)
	_ = e.p.Notify(ctx)
	if e.count(t) != 1 {
		t.Fatal("remote event written but not recorded in the ledger")
	}
}

// Ruling 18a: providers that keep only a body preview still match.
func TestTruncatedBodyEchoIsSuppressed(t *testing.T) {
	e := newEnv(t, baseCfg+`  - id: out
    inputs: [{calendar: feed}]
    target: {calendar: work}
`, "work")
	ctx := context.Background()
	body := strings.Repeat("abcdefghij", 40) // 400 chars
	s := e.now.Add(time.Hour)
	if err := e.db.UpsertEvent(ctx, &store.Event{ID: "feed:primary:m", SourceID: "feed", UID: "m", Title: "Long",
		Description: body, Start: s, End: s.Add(time.Hour), Status: "confirmed"}); err != nil {
		t.Fatal(err)
	}
	if err := e.p.Notify(ctx); err != nil {
		t.Fatal(err)
	}
	var rid string
	for id := range e.conns["work"].remote {
		rid = id
	}
	echo := func(title, desc string) {
		_ = e.db.UpsertEvent(ctx, &store.Event{ID: store.EventID("work", "primary", rid), SourceID: "work", UID: rid,
			Title: title, Description: desc, Start: s, End: s.Add(time.Hour), Status: "confirmed"})
	}
	list := func() int {
		evs, _ := e.db.ListEvents(ctx, e.now.Add(-time.Hour), e.now.Add(48*time.Hour))
		n := 0
		for _, x := range evs {
			if x.SourceID == "work" {
				n++
			}
		}
		return n
	}
	echo("Long", body[:255])
	if list() != 0 {
		t.Fatal("preview-truncated echo was ingested")
	}
	echo("Long (edited by me)", body[:255])
	if list() != 1 {
		t.Fatal("genuine remote edit was not ingested")
	}
}

// Ruling 18b: replans regenerate block ids; identical content must not churn.
func TestReplanWithNewBlockIDsDoesNotChurn(t *testing.T) {
	e := newEnv(t, baseCfg+outFocacal, "work")
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		e.block(t, fmt.Sprintf("old%d", i), "Focus", time.Duration(i+1)*3*time.Hour)
	}
	if err := e.p.Notify(ctx); err != nil {
		t.Fatal(err)
	}
	w := e.conns["work"]
	for i := 0; i < 3; i++ {
		_ = e.db.DeleteBlock(ctx, fmt.Sprintf("old%d", i))
		e.block(t, fmt.Sprintf("new%d", i), "Focus", time.Duration(i+1)*3*time.Hour)
	}
	if err := e.p.Notify(ctx); err != nil {
		t.Fatal(err)
	}
	if len(w.writes) != 3 || len(w.dels) != 0 {
		t.Fatalf("churn: writes=%d dels=%d", len(w.writes), len(w.dels))
	}
}

const outFeed = `  - id: out
    inputs: [{calendar: feed}]
    target: {calendar: work}
`

func (e *env) timedEvent(t *testing.T, uid string, start time.Time, length time.Duration) {
	t.Helper()
	if err := e.db.UpsertEvent(context.Background(), &store.Event{ID: store.EventID("feed", "primary", uid), SourceID: "feed",
		CalendarID: "primary", UID: uid, Title: uid, Start: start, End: start.Add(length), Status: "confirmed"}); err != nil {
		t.Fatal(err)
	}
}

// Ruling 19: the projection window filters on start time, so an item whose
// start slid out of the window while its end is still inside is history, not
// a removal. Hourly pushes across the boundary must never delete.
func TestPusherDoesNotDeleteItemsStraddlingWindowStart(t *testing.T) {
	e := newEnv(t, baseCfg+outFeed, "work")
	ctx := context.Background()
	base := e.now
	s := base.Add(time.Hour)
	e.timedEvent(t, "multiday", s, 3*24*time.Hour)
	e.timedEvent(t, "hour", s, 90*time.Minute)
	if err := e.p.Notify(ctx); err != nil {
		t.Fatal(err)
	}
	w := e.conns["work"]
	if len(w.remote) != 2 {
		t.Fatalf("setup wrote %d", len(w.remote))
	}
	// winStart = clock-7d walks from base to base+4h, crossing the start.
	for h := 0; h <= 4; h++ {
		e.now = base.Add(7*24*time.Hour + time.Duration(h)*time.Hour)
		if err := e.p.Notify(ctx); err != nil {
			t.Fatal(err)
		}
		if len(w.dels) != 0 || len(w.remote) != 2 || e.count(t) != 2 {
			t.Fatalf("step %dh deleted a straddling item: dels=%v remote=%d ledger=%d", h, w.dels, len(w.remote), e.count(t))
		}
	}
}

func TestPusherDeletesGenuinelyRemovedInWindowItem(t *testing.T) {
	e := newEnv(t, baseCfg+outFeed, "work")
	ctx := context.Background()
	e.timedEvent(t, "m", e.now.Add(time.Hour), time.Hour)
	if err := e.p.Notify(ctx); err != nil {
		t.Fatal(err)
	}
	_ = e.db.DeleteEvent(ctx, store.EventID("feed", "primary", "m"))
	if err := e.p.Notify(ctx); err != nil {
		t.Fatal(err)
	}
	if len(e.conns["work"].dels) != 1 || e.count(t) != 0 {
		t.Fatal("a removed in-window item must be deleted")
	}
}

func TestPusherNeverDeletesRowsWithZeroTimes(t *testing.T) {
	e := newEnv(t, baseCfg+outFeed, "work")
	ctx := context.Background()
	w := e.conns["work"]
	in := e.now.Add(time.Hour)
	for i, le := range []store.LedgerEntry{
		{StartsAt: time.Time{}, EndsAt: in.Add(time.Hour)},
		{StartsAt: in, EndsAt: time.Time{}},
	} {
		id := fmt.Sprintf("r%d", i)
		w.remote[id] = connector.RemoteEvent{}
		le.CalendarID, le.RemoteID, le.DestinationID, le.ItemKey, le.Hash = "work", id, "out", "k"+id, "h"
		if err := e.db.LedgerPut(ctx, le); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.p.Notify(ctx); err != nil {
		t.Fatal(err)
	}
	if len(w.dels) != 0 || e.count(t) != 2 {
		t.Fatalf("zero-time rows deleted: dels=%v ledger=%d", w.dels, e.count(t))
	}
}
