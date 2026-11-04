package connector

import (
	"maps"
	"slices"
	"sync"
)

// Registry holds the live set of connectors keyed by source id. It is safe for
// concurrent use; readers take copies so a Replace never races a sync in flight.
type Registry struct {
	mu    sync.RWMutex
	conns map[string]Connector

	lockMu  sync.Mutex // guards srcLock only; never held while waiting on a source lock
	srcLock map[string]*sync.Mutex
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{conns: map[string]Connector{}}
}

// Get returns the connector for id. A nil Registry has no connectors.
func (r *Registry) Get(id string) (Connector, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.conns[id]
	return c, ok
}

// IDs returns the registered source ids, sorted.
func (r *Registry) IDs() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return slices.Sorted(maps.Keys(r.conns))
}

// Snapshot returns a copy of the current id-to-connector map.
func (r *Registry) Snapshot() map[string]Connector {
	if r == nil {
		return map[string]Connector{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return maps.Clone(r.conns)
}

// Replace atomically swaps in a copy of next.
func (r *Registry) Replace(next map[string]Connector) {
	cp := maps.Clone(next)
	if cp == nil {
		cp = map[string]Connector{}
	}
	r.mu.Lock()
	r.conns = cp
	r.mu.Unlock()
}

// LockSource takes the per-source-id lock and returns its unlock func. It
// serialises everything that touches one source's stored state (syncs, purges)
// across connector instances that share the id, so a replaced or re-added
// source cannot interleave with its predecessor. It is independent of the
// registry's read/write lock, so holding it while calling Get is safe.
func (r *Registry) LockSource(id string) (unlock func()) {
	r.lockMu.Lock()
	if r.srcLock == nil {
		r.srcLock = map[string]*sync.Mutex{}
	}
	m, ok := r.srcLock[id]
	if !ok {
		m = &sync.Mutex{}
		r.srcLock[id] = m
	}
	r.lockMu.Unlock()
	m.Lock()
	return m.Unlock
}
