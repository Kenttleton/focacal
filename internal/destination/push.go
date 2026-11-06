package destination

import (
	"context"
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
		now: time.Now, locks: map[string]*sync.Mutex{}}
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

// Notify pushes every calendar-target destination from one config snapshot.
// A destination that fails does not stop the others; the returned error names
// only the failed destination ids.
func (p *Pusher) Notify(ctx context.Context) error {
	cfg := p.live.Current()
	var targets []config.DestinationConfig
	for _, d := range cfg.Destinations {
		if d.Target.Calendar != "" {
			targets = append(targets, d)
		}
	}
	if len(targets) == 0 {
		return nil
	}

	now := p.now()
	from, to := now.Add(-pushBack), now.Add(pushForward)
	events, err := p.db.ListEvents(ctx, from, to)
	if err != nil {
		return fmt.Errorf("list events: %w", err)
	}
	blocks, err := p.db.ListBlocks(ctx, from, to)
	if err != nil {
		return fmt.Errorf("list blocks: %w", err)
	}

	var errs []error
	for _, d := range targets {
		if err := p.pushOne(ctx, cfg, d, events, blocks); err != nil {
			// Provider errors can embed URLs or secrets: log the id only.
			p.log.Warn("push destination failed", "destination", d.ID)
			errs = append(errs, fmt.Errorf("destination %s", d.ID))
		}
	}
	return errors.Join(errs...)
}

// item is one remote event a destination should hold.
type item struct {
	key  string
	ev   connector.RemoteEvent
	hash string
}

func (p *Pusher) pushOne(ctx context.Context, cfg *config.Config, d config.DestinationConfig, events []*store.Event, blocks []*store.Block) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.New("panic")
		}
	}()
	target := d.Target.Calendar

	l := p.destLock(d.ID)
	l.Lock()
	defer l.Unlock()

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

	pe, pb := Project(cfg, d, events, blocks)
	var items []item
	for _, e := range pe {
		if e.AllDay { // ruling 11: RemoteEvent carries only times
			continue
		}
		items = append(items, item{key: e.ID, hash: store.FieldHash(e.Title, e.Description, e.Start, e.End),
			ev: connector.RemoteEvent{Title: e.Title, Description: e.Description, Location: e.Location,
				Start: e.Start, End: e.End, ShowAs: "busy", ManagedTag: true}})
	}
	for _, b := range pb {
		items = append(items, item{key: b.ID, hash: store.FieldHash(b.Title, "", b.Start, b.End),
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
	wanted := make(map[string]bool, len(items))
	for _, it := range items {
		wanted[it.key] = true
		prev, had := byKey[it.key]
		if had && prev.CalendarID != target {
			had = false // written to an earlier target; stale rows are removed below
		}
		if had && prev.Hash == it.hash {
			continue // unchanged: idempotent
		}
		ev := it.ev
		if had {
			ev.RemoteID = prev.RemoteID
		}
		remoteID, werr := conn.WriteEvent(ctx, &ev)
		if werr != nil {
			failed = true
			continue
		}
		if perr := p.db.LedgerPut(ctx, store.LedgerEntry{CalendarID: target, RemoteID: remoteID,
			DestinationID: d.ID, ItemKey: it.key, Hash: it.hash, WrittenAt: p.now()}); perr != nil {
			failed = true
		}
	}

	for _, le := range ledger {
		if le.CalendarID == target && wanted[le.ItemKey] {
			continue
		}
		c := conn
		if le.CalendarID != target {
			var ok bool
			if c, ok = p.lookup(le.CalendarID); !ok {
				// The old target is gone; nothing remote to delete.
				_ = p.db.LedgerDelete(ctx, le.CalendarID, le.RemoteID)
				continue
			}
		}
		if derr := c.DeleteEvent(ctx, le.RemoteID); derr != nil {
			failed = true
			continue
		}
		if lerr := p.db.LedgerDelete(ctx, le.CalendarID, le.RemoteID); lerr != nil {
			failed = true
		}
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
