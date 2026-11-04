package config

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	defaultDebounce  = 200 * time.Millisecond
	defaultPollEvery = 5 * time.Second
)

// dirWatcher is the slice of fsnotify.Watcher that Watch uses; tests fake it.
type dirWatcher interface {
	Add(path string) error
	Close() error
	events() <-chan fsnotify.Event
	errors() <-chan error
}

type fsWatcher struct{ *fsnotify.Watcher }

func (w fsWatcher) events() <-chan fsnotify.Event { return w.Events }
func (w fsWatcher) errors() <-chan error          { return w.Errors }

func newFSWatcher() (dirWatcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return fsWatcher{w}, nil
}

// fileSig is what the poll compares: existence, size and mtime of one file.
type fileSig struct {
	exists bool
	size   int64
	mtime  time.Time
	sum    [sha256.Size]byte // content hash: catches same-size edits within one coarse mtime tick
}

// watchedNames are the only files that matter: the domain files and the
// credentials file.
func watchedNames() map[string]bool {
	names := map[string]bool{CredentialsFile: true}
	for _, d := range Domains {
		names[d.File()] = true
	}
	return names
}

func (l *Live) statFiles(names map[string]bool) map[string]fileSig {
	sigs := make(map[string]fileSig, len(names))
	for n := range names {
		if fi, err := os.Stat(filepath.Join(l.dir, n)); err == nil {
			sig := fileSig{exists: true, size: fi.Size(), mtime: fi.ModTime()}
			if b, err := os.ReadFile(filepath.Join(l.dir, n)); err == nil { // files are tiny
				sig.sum = sha256.Sum256(b)
			}
			sigs[n] = sig
		} else {
			sigs[n] = fileSig{}
		}
	}
	return sigs
}

func sigsEqual(a, b map[string]fileSig) bool {
	for n, s := range a {
		o := b[n]
		if s.exists != o.exists || s.size != o.size || !s.mtime.Equal(o.mtime) || s.sum != o.sum {
			return false
		}
	}
	return true
}

// Watch reloads the config when the config directory changes, until ctx is
// cancelled (it then returns nil). It watches the directory, not the files,
// because editors and atomic writes replace the inode. Events are debounced,
// and a periodic stat poll catches changes a bind mount never reports. If
// the watcher cannot be set up it logs a warning and polls alone. A failed
// reload is not a Watch error: Live records it and keeps the old config.
func (l *Live) Watch(ctx context.Context) error {
	debounce, pollEvery := l.debounce, l.pollEvery
	if debounce <= 0 {
		debounce = defaultDebounce
	}
	if pollEvery <= 0 {
		pollEvery = defaultPollEvery
	}
	names := watchedNames()

	var events <-chan fsnotify.Event
	var errs <-chan error
	newWatcher := l.newWatcher
	if newWatcher == nil {
		newWatcher = newFSWatcher
	}
	w, err := newWatcher()
	if err == nil {
		if err = w.Add(l.dir); err != nil {
			_ = w.Close()
		}
	}
	if err != nil {
		l.log.Warn("config directory watcher unavailable; polling only", "dir", l.dir, "err", err)
	} else {
		defer w.Close()
		events, errs = w.events(), w.errors()
	}

	last := l.statFiles(names)
	poll := time.NewTicker(pollEvery)
	defer poll.Stop()
	timer := time.NewTimer(debounce)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	pending := false

	reload := func() {
		last = l.statFiles(names) // before the reload, so a racing write is seen next poll
		_, _ = l.Reload()
		if l.reloadHook != nil {
			l.reloadHook()
		}
	}
	// Catch anything that changed between NewLive and the watch being armed.
	reload()
	schedule := func() {
		if pending && !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(debounce)
		pending = true
	}
	const relevant = fsnotify.Write | fsnotify.Create | fsnotify.Rename | fsnotify.Remove

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-events:
			if !ok {
				events, errs = nil, nil
				continue
			}
			if ev.Op&relevant == 0 || !names[filepath.Base(ev.Name)] {
				continue
			}
			schedule()
		case err, ok := <-errs:
			if !ok {
				events, errs = nil, nil
				continue
			}
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				// Events were dropped: reload instead of trusting what we saw.
				schedule()
			}
			if err != nil {
				l.log.Warn("config directory watcher error", "err", err)
			}
		case <-timer.C:
			pending = false
			reload()
		case <-poll.C:
			// Same debounce as events, so a file caught half-written settles
			// before it is read.
			if cur := l.statFiles(names); !sigsEqual(cur, last) {
				last = cur
				schedule()
			}
		}
	}
}
