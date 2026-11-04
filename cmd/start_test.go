package cmd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/Kenttleton/focacal/internal/bus"
	"github.com/Kenttleton/focacal/internal/config"
	"github.com/Kenttleton/focacal/internal/connector"
)

type fakeConn struct {
	id       string
	redirect string
	syncHook func()
	synced   int
}

func (f *fakeConn) SourceID() string { return f.id }
func (f *fakeConn) Sync(context.Context) error {
	f.synced++
	if f.syncHook != nil {
		f.syncHook()
	}
	return nil
}
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
	if g.(*fakeConn).redirect != "https://b" {
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
	c.syncHook = func() { reg.Replace(map[string]connector.Connector{}) } // removed mid-sync
	reg.Replace(map[string]connector.Connector{"a": c})
	syncOnly(context.Background(), discardLog(), reg, []string{"a"}, b)
	select {
	case m := <-got:
		t.Fatalf("published %v for a removed source", m.Type)
	case <-time.After(100 * time.Millisecond):
	}
	if c.synced != 1 {
		t.Fatalf("synced = %d", c.synced)
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
	if a.synced != 1 || o.synced != 0 {
		t.Fatalf("synced a=%d o=%d", a.synced, o.synced)
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
