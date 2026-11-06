package destination

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Kenttleton/focacal/internal/config"
	"github.com/Kenttleton/focacal/internal/connector"
	"github.com/Kenttleton/focacal/internal/store"
)

// Push window: the same -7 to +14 days the connectors sync.
const (
	pushBack    = 7 * 24 * time.Hour
	pushForward = 14 * 24 * time.Hour
)

// ConnectorLookup finds the connector of a target calendar.
type ConnectorLookup func(calendarID string) (connector.Connector, bool)

// Pusher writes the projection of every destination whose target is a
// calendar to that calendar, and records each write in the store's ledger so
// ingest ignores the echo.
type Pusher struct {
	live       *config.Live
	lookup     ConnectorLookup
	lockSource func(calendarID string) (unlock func())
	db         *store.DB
	log        *slog.Logger
	now        func() time.Time

	mu    sync.Mutex
	locks map[string]*sync.Mutex // per destination id
	fps   map[string]string      // last pushed config fingerprint per destination
}

// NewPusher builds a Pusher. lockSource takes the per-calendar lock the
// connector registry offers (Registry.LockSource), held around writes so a
// push cannot interleave with a sync or purge of the same calendar; nil
// means no locking. A nil log discards logs.
func NewPusher(live *config.Live, lookup ConnectorLookup, lockSource func(calendarID string) (unlock func()), db *store.DB, log *slog.Logger) *Pusher {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Pusher{live: live, lookup: lookup, lockSource: lockSource, db: db, log: log,
		now: time.Now, locks: map[string]*sync.Mutex{}, fps: map[string]string{}}
}

func (p *Pusher) destLock(id string) *sync.Mutex {
	p.mu.Lock()
	defer p.mu.Unlock()
	m, ok := p.locks[id]
	if !ok {
		m = &sync.Mutex{}
		p.locks[id] = m
	}
	return m
}

// Notify pushes every calendar-target destination. A destination that fails
// does not stop the others; the returned error names only the failed
// destination ids. Each destination reads its own config snapshot, events and
// blocks under its lock, so a push that waited for an earlier one never
// writes stale data.
func (p *Pusher) Notify(ctx context.Context) error {
	var ids []string
	for _, d := range p.live.Current().Destinations {
		if d.Target.Calendar != "" {
			ids = append(ids, d.ID)
		}
	}
	var errs []error
	for _, id := range ids {
		if err := p.pushOne(ctx, id); err != nil {
			// Provider errors can embed URLs or secrets: log the id only.
			p.log.Warn("push destination failed", "destination", id)
			errs = append(errs, fmt.Errorf("destination %s", id))
		}
	}
	return errors.Join(errs...)
}

// item is one remote event a destination should hold.
type item struct {
	key       string
	ev        connector.RemoteEvent
	hash      string
	hashTrunc string
	endsAt    time.Time
}

// Mass-delete guard (ruling 14) and history retention (ruling 13).
const (
	maxRemovalsUnchecked = 5
	forgetAfter          = 30 * 24 * time.Hour
	ledgerTimeout        = 10 * time.Second
)

// blockKey is the content-derived item key of a planner block: the planner
// gives blocks fresh ids every replan, which must not churn remote events.
func blockKey(b *store.Block) string {
	const f = time.RFC3339
	return "block:" + string(b.Type) + ":" + b.Start.UTC().Format(f) + ":" + b.End.UTC().Format(f)
}

// fingerprint identifies the parts of a destination's config that decide what
// it publishes and where.
func fingerprint(cfg *config.Config, d config.DestinationConfig) string {
	b, _ := json.Marshal(struct {
		Inputs []config.DestinationInput
		Target config.DestinationTarget
	}{cfg.ExpandInputs(d), d.Target})
	return string(b)
}

func (p *Pusher) pushOne(ctx context.Context, id string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.New("panic")
		}
	}()

	l := p.destLock(id)
	l.Lock()
	defer l.Unlock()

	// Everything below is read under the lock.
	cfg := p.live.Current()
	var d config.DestinationConfig
	found := false
	for _, x := range cfg.Destinations {
		if x.ID == id && x.Target.Calendar != "" {
			d, found = x, true
		}
	}
	if !found {
		return nil // removed or retargeted to a served feed since Notify began
	}
	target := d.Target.Calendar

	fp := fingerprint(cfg, d)
	p.mu.Lock()
	prevFP, seen := p.fps[id]
	p.fps[id] = fp
	p.mu.Unlock()
	configChanged := seen && prevFP != fp

	if !writableTarget(cfg, target) {
		return errors.New("target not writable")
	}
	if p.lockSource != nil {
		defer p.lockSource(target)()
	}
	conn, ok := p.lookup(target)
	if !ok {
		return errors.New("no connector")
	}

	now := p.now()
	winStart, winEnd := now.Add(-pushBack), now.Add(pushForward)
	events, err := p.db.ListEvents(ctx, winStart, winEnd)
	if err != nil {
		return err
	}
	blocks, err := p.db.ListBlocks(ctx, winStart, winEnd)
	if err != nil {
		return err
	}

	pe, pb := Project(cfg, d, events, blocks)
	var items []item
	seenKey := map[string]bool{}
	add := func(it item) {
		if !seenKey[it.key] {
			seenKey[it.key] = true
			items = append(items, it)
		}
	}
	for _, e := range pe {
		if e.AllDay { // ruling 11: RemoteEvent carries only times
			continue
		}
		add(item{key: e.ID, hash: store.FieldHash(e.Title, e.Description, e.Start, e.End),
			hashTrunc: store.FieldHashTrunc(e.Title, e.Description, e.Start, e.End), endsAt: e.End,
			ev: connector.RemoteEvent{Title: e.Title, Description: e.Description, Location: e.Location,
				Start: e.Start, End: e.End, ShowAs: "busy", ManagedTag: true}})
	}
	for _, b := range pb {
		add(item{key: blockKey(b), hash: store.FieldHash(b.Title, "", b.Start, b.End),
			hashTrunc: store.FieldHashTrunc(b.Title, "", b.Start, b.End), endsAt: b.End,
			ev: connector.RemoteEvent{Title: b.Title, Start: b.Start, End: b.End, ShowAs: "busy", ManagedTag: true}})
	}

	ledger, err := p.db.LedgerByDestination(ctx, d.ID)
	if err != nil {
		return err
	}
	byKey := make(map[string]store.LedgerEntry, len(ledger))
	for _, le := range ledger {
		byKey[le.ItemKey] = le
	}

	failed := false
	// Ledger changes after a remote change must survive an expired parent
	// context, or the remote event would be left untracked.
	record := func(f func(context.Context) error) {
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), ledgerTimeout)
		defer cancel()
		if err := f(c); err != nil {
			p.log.Warn("ledger update failed", "destination", d.ID)
			failed = true
		}
	}

	wanted := make(map[string]bool, len(items))
	for _, it := range items {
		wanted[it.key] = true
		prev, had := byKey[it.key]
		if had && prev.CalendarID != target {
			had = false // written to an earlier target; stale rows are handled below
		}
		if had && prev.Hash == it.hash {
			continue // unchanged: idempotent
		}
		ev := it.ev
		if had {
			ev.RemoteID = prev.RemoteID
		}
		remoteID, werr := conn.WriteEvent(ctx, &ev)
		if had && errors.Is(werr, connector.ErrNotFound) {
			// The remote event is gone: drop the stale row, create anew.
			record(func(c context.Context) error { return p.db.LedgerDelete(c, prev.CalendarID, prev.RemoteID) })
			ev.RemoteID = ""
			remoteID, werr = conn.WriteEvent(ctx, &ev)
		}
		if werr != nil {
			failed = true
			continue
		}
		record(func(c context.Context) error {
			return p.db.LedgerPut(c, store.LedgerEntry{CalendarID: target, RemoteID: remoteID,
				DestinationID: d.ID, ItemKey: it.key, Hash: it.hash, HashTrunc: it.hashTrunc,
				EndsAt: it.endsAt, WrittenAt: p.now()})
		})
	}

	// Ledger rows the projection no longer produces. One that ended before
	// the window is history, not a removal: it stays on the remote (and keeps
	// suppressing its echo) until it is 30 days old, then is forgotten with
	// no remote delete.
	var removals []store.LedgerEntry
	inWindow := 0
	for _, le := range ledger {
		if !le.EndsAt.IsZero() && le.EndsAt.Before(winStart) {
			if le.EndsAt.Before(now.Add(-forgetAfter)) {
				le := le
				record(func(c context.Context) error { return p.db.LedgerDelete(c, le.CalendarID, le.RemoteID) })
			}
			continue
		}
		inWindow++
		if le.CalendarID == target && wanted[le.ItemKey] {
			continue
		}
		removals = append(removals, le)
	}
	if len(removals) > maxRemovalsUnchecked && len(removals)*2 > inWindow && !configChanged {
		p.log.Warn("push skipped deletes: too many removals at once", "destination", d.ID)
		removals = nil
		failed = true
	}
	for _, le := range removals {
		c := conn
		if le.CalendarID != target {
			var ok bool
			if c, ok = p.lookup(le.CalendarID); !ok {
				// The old target is gone; nothing remote to delete.
				le := le
				record(func(cc context.Context) error { return p.db.LedgerDelete(cc, le.CalendarID, le.RemoteID) })
				continue
			}
		}
		if derr := c.DeleteEvent(ctx, le.RemoteID); derr != nil {
			failed = true
			continue
		}
		le := le
		record(func(cc context.Context) error { return p.db.LedgerDelete(cc, le.CalendarID, le.RemoteID) })
	}
	if failed {
		return errors.New("some writes failed")
	}
	return nil
}

// writableTarget reports whether calendar id is a configured calendar a
// connector can write to: an ics (url-only) calendar is read-only.
func writableTarget(cfg *config.Config, id string) bool {
	for _, s := range cfg.Sources {
		if s.ID == id {
			return s.Type != "ics"
		}
	}
	return false
}
