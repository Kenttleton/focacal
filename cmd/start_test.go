package cmd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Kenttleton/focacal/internal/bus"
	"github.com/Kenttleton/focacal/internal/config"
	"github.com/Kenttleton/focacal/internal/connector"
	"github.com/Kenttleton/focacal/internal/store"
)

type fakeConn struct {
	id       string
	redirect string
	// onSync, if set, runs inside Sync with the Sync context.
	onSync func(ctx context.Context)

	mu         sync.Mutex
	synced     int
	inSync     int
	overlapped bool
}

func (f *fakeConn) SourceID() string { return f.id }
func (f *fakeConn) Sync(ctx context.Context) error {
	f.mu.Lock()
	f.synced++
	f.inSync++
	if f.inSync > 1 {
		f.overlapped = true
	}
	f.mu.Unlock()
	if f.onSync != nil {
		f.onSync(ctx)
	}
	f.mu.Lock()
	f.inSync--
	f.mu.Unlock()
	return ctx.Err()
}
func (f *fakeConn) syncCount() int                                { f.mu.Lock(); defer f.mu.Unlock(); return f.synced }
func (f *fakeConn) RegisterWebhook(context.Context, string) error { return nil }
func (f *fakeConn) ValidateWebhook(context.Context, []byte, map[string]string) (string, error) {
	return f.id, nil
}
func (f *fakeConn) WriteEvent(context.Context, *connector.RemoteEvent) (string, error) {
	return "", nil
}
func (f *fakeConn) DeleteEvent(context.Context, string) error { return nil }

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func cfgWith(srcs ...config.SourceConfig) *config.Config {
	c := &config.Config{}
	c.Sources = srcs
	return c
}

func fakeBuild(failIDs ...string) (buildFunc, *[]string) {
	var built []string
	return func(src config.SourceConfig, redirect string) (connector.Connector, error) {
		built = append(built, src.ID)
		if slices.Contains(failIDs, src.ID) {
			return nil, errors.New("boom")
		}
		return &fakeConn{id: src.ID, redirect: redirect}, nil
	}, &built
}

func TestReconcileAddsNewSource(t *testing.T) {
	reg := connector.NewRegistry()
	build, _ := fakeBuild()
	added := reconcileConnectors(reg, nil, cfgWith(config.SourceConfig{ID: "a", Type: "ics"}), build, discardLog())
	if !reflect.DeepEqual(added, []string{"a"}) {
		t.Fatalf("added = %v", added)
	}
	if _, ok := reg.Get("a"); !ok {
		t.Fatal("a not registered")
	}
}

func TestReconcileRebuildsChangedSource(t *testing.T) {
	reg := connector.NewRegistry()
	build, _ := fakeBuild()
	old := cfgWith(config.SourceConfig{ID: "a", Type: "ics", Label: "x"})
	reconcileConnectors(reg, nil, old, build, discardLog())
	before, _ := reg.Get("a")
	next := cfgWith(config.SourceConfig{ID: "a", Type: "ics", Label: "y"})
	added := reconcileConnectors(reg, old, next, build, discardLog())
	after, _ := reg.Get("a")
	if !reflect.DeepEqual(added, []string{"a"}) || before == after {
		t.Fatalf("added=%v same=%v, want rebuilt", added, before == after)
	}
}

func TestReconcileRedirectBaseChangeRebuildsOAuthSources(t *testing.T) {
	reg := connector.NewRegistry()
	build, _ := fakeBuild()
	old := cfgWith(config.SourceConfig{ID: "g", Type: "google"}, config.SourceConfig{ID: "i", Type: "ics"})
	old.OAuth.RedirectBaseURL = "https://a"
	reconcileConnectors(reg, nil, old, build, discardLog())
	next := cfgWith(old.Sources...)
	next.OAuth.RedirectBaseURL = "https://b"
	added := reconcileConnectors(reg, old, next, build, discardLog())
	if !reflect.DeepEqual(added, []string{"g"}) {
		t.Fatalf("added = %v, want [g]", added)
	}
	g, _ := reg.Get("g")
	if g.(*managed).Connector.(*fakeConn).redirect != "https://b" {
		t.Fatal("rebuilt connector did not get new redirect base")
	}
}

func TestReconcileKeepsUntouchedConnector(t *testing.T) {
	reg := connector.NewRegistry()
	build, built := fakeBuild()
	old := cfgWith(config.SourceConfig{ID: "a", Type: "ics"}, config.SourceConfig{ID: "b", Type: "ics"})
	reconcileConnectors(reg, nil, old, build, discardLog())
	before, _ := reg.Get("a")
	next := cfgWith(old.Sources[0], config.SourceConfig{ID: "b", Type: "ics", Label: "changed"})
	*built = nil
	reconcileConnectors(reg, old, next, build, discardLog())
	after, _ := reg.Get("a")
	if before != after {
		t.Fatal("untouched connector was replaced")
	}
	if !reflect.DeepEqual(*built, []string{"b"}) {
		t.Fatalf("built = %v, want only b", *built)
	}
}

func TestReconcileDropsRemoved(t *testing.T) {
	reg := connector.NewRegistry()
	build, _ := fakeBuild()
	old := cfgWith(config.SourceConfig{ID: "a", Type: "ics"}, config.SourceConfig{ID: "b", Type: "ics"})
	reconcileConnectors(reg, nil, old, build, discardLog())
	added := reconcileConnectors(reg, old, cfgWith(old.Sources[1]), build, discardLog())
	if len(added) != 0 {
		t.Fatalf("added = %v", added)
	}
	if !reflect.DeepEqual(reg.IDs(), []string{"b"}) {
		t.Fatalf("ids = %v", reg.IDs())
	}
}

func TestReconcileSkipsFailingBuild(t *testing.T) {
	reg := connector.NewRegistry()
	build, _ := fakeBuild("bad")
	cfg := cfgWith(config.SourceConfig{ID: "a", Type: "ics"}, config.SourceConfig{ID: "bad", Type: "ics"}, config.SourceConfig{ID: "c", Type: "ics"})
	added := reconcileConnectors(reg, nil, cfg, build, discardLog())
	if !reflect.DeepEqual(added, []string{"a", "c"}) {
		t.Fatalf("added = %v", added)
	}
	if !reflect.DeepEqual(reg.IDs(), []string{"a", "c"}) {
		t.Fatalf("ids = %v", reg.IDs())
	}
}

func TestSyncDroppedConnectorDoesNotPublish(t *testing.T) {
	reg := connector.NewRegistry()
	b := bus.New(10 * time.Millisecond)
	got := make(chan bus.Message, 4)
	b.Subscribe(func(m bus.Message) { got <- m })
	c := &fakeConn{id: "a"}
	c.onSync = func(context.Context) { reg.Replace(map[string]connector.Connector{}) } // removed mid-sync
	reg.Replace(map[string]connector.Connector{"a": c})
	syncOnly(context.Background(), discardLog(), reg, []string{"a"}, b)
	select {
	case m := <-got:
		t.Fatalf("published %v for a removed source", m.Type)
	case <-time.After(100 * time.Millisecond):
	}
	if c.syncCount() != 1 {
		t.Fatalf("synced = %d", c.syncCount())
	}
}

func TestSyncOnlyPublishesForLiveConnector(t *testing.T) {
	reg := connector.NewRegistry()
	b := bus.New(10 * time.Millisecond)
	got := make(chan bus.Message, 4)
	b.Subscribe(func(m bus.Message) { got <- m })
	a, o := &fakeConn{id: "a"}, &fakeConn{id: "o"}
	reg.Replace(map[string]connector.Connector{"a": a, "o": o})
	syncOnly(context.Background(), discardLog(), reg, []string{"a"}, b)
	select {
	case m := <-got:
		if m.Type != bus.EventCalendarChanged {
			t.Fatalf("type = %v", m.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("no change event")
	}
	if a.syncCount() != 1 || o.syncCount() != 0 {
		t.Fatalf("synced a=%d o=%d", a.syncCount(), o.syncCount())
	}
}

func TestPlannerInputsChanged(t *testing.T) {
	a, b := cfgWith(), cfgWith()
	if plannerInputsChanged(a, b) {
		t.Fatal("equal configs reported changed")
	}
	b.WorkingHours.Start = "08:00"
	if !plannerInputsChanged(a, b) {
		t.Fatal("working hours change missed")
	}
	c := cfgWith()
	c.OAuth.RedirectBaseURL = "x"
	if plannerInputsChanged(a, c) {
		t.Fatal("oauth change should not replan")
	}
}

// registerFake builds a registry whose single source "a" wraps f.
func registerFake(f *fakeConn) (*connector.Registry, *config.Config) {
	reg := connector.NewRegistry()
	cfg := cfgWith(config.SourceConfig{ID: "a", Type: "ics"})
	reconcileConnectors(reg, nil, cfg, func(config.SourceConfig, string) (connector.Connector, error) { return f, nil }, discardLog())
	return reg, cfg
}

func TestSyncSerialisedPerInstance(t *testing.T) {
	f := &fakeConn{id: "a", onSync: func(context.Context) { time.Sleep(30 * time.Millisecond) }}
	reg, _ := registerFake(f)
	b := bus.New(10 * time.Millisecond)
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			syncOnly(context.Background(), discardLog(), reg, []string{"a"}, b)
		}()
	}
	wg.Wait()
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.overlapped {
		t.Fatal("Sync ran concurrently on one instance")
	}
	if f.synced != 3 {
		t.Fatalf("synced = %d, want 3 (waiters still run)", f.synced)
	}
}

func testDroppedSyncCancelled(t *testing.T, next func(reg *connector.Registry, old *config.Config)) {
	t.Helper()
	started := make(chan struct{})
	cancelled := make(chan struct{})
	f := &fakeConn{id: "a", onSync: func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		close(cancelled)
	}}
	reg, old := registerFake(f)
	b := bus.New(10 * time.Millisecond)
	got := make(chan bus.Message, 4)
	b.Subscribe(func(m bus.Message) { got <- m })
	done := make(chan struct{})
	go func() {
		syncOnly(context.Background(), discardLog(), reg, []string{"a"}, b)
		close(done)
	}()
	<-started
	next(reg, old)
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("in-flight Sync context was not cancelled")
	}
	<-done
	select {
	case m := <-got:
		t.Fatalf("published %v for a dropped instance", m.Type)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestReconcileRemoveCancelsInFlightSync(t *testing.T) {
	testDroppedSyncCancelled(t, func(reg *connector.Registry, old *config.Config) {
		reconcileConnectors(reg, old, cfgWith(), nil, discardLog())
	})
}

func TestReconcileReplaceCancelsInFlightSync(t *testing.T) {
	testDroppedSyncCancelled(t, func(reg *connector.Registry, old *config.Config) {
		changed := cfgWith(config.SourceConfig{ID: "a", Type: "ics", Label: "new"})
		build, _ := fakeBuild()
		reconcileConnectors(reg, old, changed, build, discardLog())
	})
}

func TestReconcileBuildErrorNotLogged(t *testing.T) {
	var buf strings.Builder
	log := slog.New(slog.NewTextHandler(&buf, nil))
	secret := "https://user:hunter2@cal.example.com/dav"
	build := func(config.SourceConfig, string) (connector.Connector, error) {
		return nil, errors.New("dial " + secret)
	}
	reconcileConnectors(connector.NewRegistry(), nil, cfgWith(config.SourceConfig{ID: "a", Type: "caldav"}), build, log)
	out := buf.String()
	if !strings.Contains(out, "a") || strings.Contains(out, "hunter2") || strings.Contains(out, "example.com") {
		t.Fatalf("log output leaks or lacks id: %q", out)
	}
}

func TestPurgeDroppedWaitsForCancelledSync(t *testing.T) {
	var mu sync.Mutex
	var order []string
	note := func(s string) { mu.Lock(); order = append(order, s); mu.Unlock() }
	started := make(chan struct{})
	f := &fakeConn{id: "a", onSync: func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond) // a cancelled Sync still finishing its writes
		note("sync-write")
	}}
	reg, old := registerFake(f)
	b := bus.New(10 * time.Millisecond)
	go syncOnly(context.Background(), discardLog(), reg, []string{"a"}, b)
	<-started
	_, dropped := reconcile(reg, old, cfgWith(), nil, discardLog())
	if len(dropped) != 1 || dropped[0].id != "a" {
		t.Fatalf("dropped = %v", dropped)
	}
	n := purgeDropped(context.Background(), discardLog(), reg, dropped, func(_ context.Context, id string) (int, error) {
		note("purge " + id)
		return 3, nil
	})
	mu.Lock()
	defer mu.Unlock()
	if n != 3 || !reflect.DeepEqual(order, []string{"sync-write", "purge a"}) {
		t.Fatalf("n=%d order=%v, want purge after sync finished", n, order)
	}
}

func TestReconcileRebuiltSourceIsNotPurged(t *testing.T) {
	f := &fakeConn{id: "a"}
	reg, old := registerFake(f)
	build, _ := fakeBuild()
	_, dropped := reconcile(reg, old, cfgWith(config.SourceConfig{ID: "a", Type: "ics", Label: "new"}), build, discardLog())
	if len(dropped) != 0 {
		t.Fatalf("rebuilt source reported dropped: %v", dropped)
	}
}

func TestSyncErrorNotLogged(t *testing.T) {
	var buf strings.Builder
	log := slog.New(slog.NewTextHandler(&buf, nil))
	reg := connector.NewRegistry()
	reg.Replace(map[string]connector.Connector{"a": errConn{&fakeConn{id: "a"}}})
	syncAll(context.Background(), log, reg, bus.New(time.Millisecond))
	if strings.Contains(buf.String(), "hunter2") || !strings.Contains(buf.String(), "source=a") {
		t.Fatalf("log = %q", buf.String())
	}
}

type errConn struct{ *fakeConn }

func (errConn) Sync(context.Context) error { return errors.New("GET https://u:hunter2@x.example/cal") }

func openTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func testEvent(src, uid string) *store.Event {
	now := time.Now().UTC()
	return &store.Event{ID: store.EventID(src, "c", uid), SourceID: src, CalendarID: "c", UID: uid,
		Start: now, End: now.Add(time.Hour), Status: "confirmed"}
}

func eventCount(t *testing.T, db *store.DB) int {
	t.Helper()
	evs, err := db.ListEvents(context.Background(), time.Now().Add(-time.Hour), time.Now().Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return len(evs)
}

// A removed source that is re-added while the old Sync is still finishing
// (ignoring ctx) must not have its fresh rows wiped by the old purge.
func TestPurgeSkippedWhenSourceReAdded(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	release := make(chan struct{})
	started := make(chan struct{})
	oldConn := &fakeConn{id: "a", onSync: func(context.Context) { close(started); <-release }} // ignores ctx
	reg, old := registerFake(oldConn)
	b := bus.New(10 * time.Millisecond)
	replans := make(chan bus.Message, 4)
	b.Subscribe(func(m bus.Message) { replans <- m })

	go syncOnly(ctx, discardLog(), reg, []string{"a"}, b)
	<-started
	empty := cfgWith()
	_, dropped := reconcile(reg, old, empty, nil, discardLog())
	purged := make(chan int, 1)
	go func() { purged <- purgeDropped(ctx, discardLog(), reg, dropped, purgeSource(db)) }()

	newConn := &fakeConn{id: "a", onSync: func(context.Context) {
		if err := db.UpsertEvent(ctx, testEvent("a", "fresh")); err != nil {
			t.Error(err)
		}
	}}
	reconcile(reg, empty, cfgWith(config.SourceConfig{ID: "a", Type: "ics"}),
		func(config.SourceConfig, string) (connector.Connector, error) { return newConn, nil }, discardLog())
	syncDone := make(chan struct{})
	go func() { syncOnly(ctx, discardLog(), reg, []string{"a"}, b); close(syncDone) }()

	time.Sleep(50 * time.Millisecond) // both queued behind the old Sync
	close(release)
	if n := <-purged; n != 0 {
		t.Fatalf("purged %d rows of a re-added source", n)
	}
	<-syncDone
	if got := eventCount(t, db); got != 1 {
		t.Fatalf("events = %d, want the new instance's row to survive", got)
	}
}

func TestPlainRemovePurgesEventsAndSyncState(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	for _, e := range []*store.Event{testEvent("a", "1"), testEvent("b", "1")} {
		if err := db.UpsertEvent(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	for _, k := range []string{"a", "a:cal", "b", "b:cal"} {
		if err := db.SetSyncState(ctx, k, "tok", "", time.Time{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.SetOAuthToken(ctx, "a", "{}"); err != nil {
		t.Fatal(err)
	}
	reg := connector.NewRegistry()
	cfg := cfgWith(config.SourceConfig{ID: "a", Type: "ics"}, config.SourceConfig{ID: "b", Type: "ics"})
	build, _ := fakeBuild()
	reconcile(reg, nil, cfg, build, discardLog())
	// drop a, rebuild b
	next := cfgWith(config.SourceConfig{ID: "b", Type: "ics", Label: "changed"})
	_, dropped := reconcile(reg, cfg, next, build, discardLog())
	if n := purgeDropped(ctx, discardLog(), reg, dropped, purgeSource(db)); n != 1 {
		t.Fatalf("purged = %d, want 1", n)
	}
	if got := eventCount(t, db); got != 1 {
		t.Fatalf("events = %d, want only b's", got)
	}
	states, _ := db.ListSyncStates(ctx)
	var ids []string
	for _, s := range states {
		ids = append(ids, s.SourceID)
	}
	if !reflect.DeepEqual(ids, []string{"b", "b:cal"}) {
		t.Fatalf("sync states = %v, want only b's (rebuilt source untouched)", ids)
	}
	if tok, _ := db.GetOAuthToken(ctx, "a"); tok == "" {
		t.Fatal("oauth token was deleted")
	}
}

// A reload that changes planner values publishes a replan request; one that
// changes nothing the planner reads does not.
func TestPlannerReloadPublishesReplanRequest(t *testing.T) {
	dir := t.TempDir()
	write := func(end string) {
		for name, body := range map[string]string{
			"config.yaml":    "server:\n  mcp_port: 5234\n",
			"planner.yaml":   "working_hours:\n  start: \"09:00\"\n  end: \"" + end + "\"\n  timezone: \"UTC\"\n",
			"calendars.yaml": "sources: []\n",
		} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	write("17:00")
	live, err := config.NewLive(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	b := bus.New(time.Millisecond)
	got := make(chan bus.Message, 4)
	b.Subscribe(func(m bus.Message) { got <- m })
	live.OnChange(func(old, cur *config.Config) { requestReplanIfPlannerChanged(b, old, cur) })

	write("18:00")
	if changed, err := live.Reload(); err != nil || !changed {
		t.Fatalf("Reload = %v, %v", changed, err)
	}
	select {
	case m := <-got:
		if m.Type != bus.EventReplanRequested {
			t.Fatalf("published %v, want replan request", m.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("planner reload did not publish a replan request")
	}
}
