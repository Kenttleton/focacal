package config

import (
	"io"
	"log/slog"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
)

// snapshot is one immutable load: file is the env-free file config (used for
// restart detection and the API's saved values), running is what the process
// runs with (env applied, file paths resolved).
type snapshot struct {
	file, running *Config
}

type loadErr struct{ err error }

// Live holds the current running config and reloads it from the domain files.
// Current is lock-free; Reload is serialized.
type Live struct {
	dir string
	log *slog.Logger

	snap    atomic.Pointer[snapshot]
	lastErr atomic.Pointer[loadErr]
	server  ServerConfig // file-level server block at NewLive

	reloadMu sync.Mutex // serializes Reload
	notifyMu sync.Mutex // serializes subscriber delivery in swap order

	subMu sync.Mutex
	subs  []func(old, cur *Config)

	// Watch tuning; zero values select the defaults. Tests set these.
	debounce   time.Duration
	pollEvery  time.Duration
	newWatcher func() (*fsnotify.Watcher, error) // nil: fsnotify.NewWatcher
	reloadHook func()                            // called after each reload Watch runs
}

// NewLive performs the first load of dir. It fails on any error: only
// reloads are lenient.
func NewLive(dir string, log *slog.Logger) (*Live, error) {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	file, running, err := loadSnapshot(dir)
	if err != nil {
		return nil, err
	}
	l := &Live{dir: dir, log: log, server: file.Server}
	l.snap.Store(&snapshot{file: file, running: running})
	return l, nil
}

// Current returns the running config. It is never nil and must be treated as
// immutable; a reload swaps in a new value rather than editing this one.
func (l *Live) Current() *Config { return l.snap.Load().running }

// fileConfig returns the env-free file config of the last good load.
func (l *Live) fileConfig() *Config { return l.snap.Load().file }

// LoadError returns the error of the last reload, nil when it succeeded.
func (l *Live) LoadError() error {
	if e := l.lastErr.Load(); e != nil {
		return e.err
	}
	return nil
}

// RestartRequired reports whether the file-level server block differs from
// the one captured at NewLive. Nothing else needs a restart.
func (l *Live) RestartRequired() bool {
	return !reflect.DeepEqual(l.fileConfig().Server, l.server)
}

// OnChange registers fn to run after a reload swaps in a different running
// config, with the old and new values. Subscribers run in registration order
// on the goroutine that called Reload. Deliveries are serialized in swap
// order, so a subscriber diffing old/new never sees them reordered. A
// subscriber may call Current but must not call Reload (it would deadlock).
// Reconciling against Current() rather than trusting cur is always safe.
func (l *Live) OnChange(fn func(old, cur *Config)) {
	l.subMu.Lock()
	defer l.subMu.Unlock()
	l.subs = append(l.subs, fn)
}

// Reload re-reads the domain files. On any error the previous config stays,
// the error is recorded (LoadError) and returned. On success the error is
// cleared and the new config swapped in; changed is true only when the new
// running config differs from the previous one, and only then do subscribers
// run.
func (l *Live) Reload() (changed bool, err error) {
	l.reloadMu.Lock()
	file, running, err := loadSnapshot(l.dir)
	if err != nil {
		l.lastErr.Store(&loadErr{err})
		l.reloadMu.Unlock()
		l.log.Warn("config reload failed; keeping the previous config", "err", err)
		return false, err
	}
	old := l.snap.Load()
	l.snap.Store(&snapshot{file: file, running: running})
	l.lastErr.Store(nil)
	changed = !reflect.DeepEqual(old.running, running)
	if changed {
		// Handoff: take notifyMu before releasing reloadMu so the next
		// Reload cannot deliver before this one.
		l.notifyMu.Lock()
	}
	l.reloadMu.Unlock()

	if changed {
		defer l.notifyMu.Unlock()
		l.subMu.Lock()
		subs := slices.Clone(l.subs)
		l.subMu.Unlock()
		for _, fn := range subs {
			fn(old.running, running)
		}
	}
	return changed, nil
}
