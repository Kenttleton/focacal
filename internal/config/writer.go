package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// renameFile is a test seam for os.Rename.
var renameFile = os.Rename

// Writer serializes and atomically applies edits to one YAML config file.
// One Writer per path should be shared; separate Writers do not share a lock.
type Writer struct {
	path string
	perm os.FileMode
	mu   sync.Mutex
}

// NewWriter returns a Writer that creates missing files with mode 0644.
func NewWriter(path string) *Writer { return NewWriterMode(path, 0644) }

// NewWriterMode returns a Writer that creates missing files with perm.
// Existing files keep their current mode.
func NewWriterMode(path string, perm os.FileMode) *Writer {
	return &Writer{path: path, perm: perm}
}

// Update reads the file's current bytes (a missing file reads as empty), calls
// fn, and writes the bytes fn returns. If fn errors the file is left
// untouched. The bytes are read under the lock on every call, so an external
// edit between two Updates is seen by the second.
//
// The write goes to a temp file in the same directory followed by a rename.
// When rename fails (e.g. EBUSY on a single-file Docker bind mount), the
// previous bytes are saved to <path>.bak and the file is rewritten in place.
func (w *Writer) Update(fn func(cur []byte) (next []byte, err error)) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	mode := w.perm
	existed := true
	data, err := os.ReadFile(w.path)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("read config %s: %w", w.path, err)
		}
		existed = false
		data = nil
	} else if st, err := os.Stat(w.path); err == nil {
		mode = st.Mode().Perm()
	}

	next, err := fn(bytes.Clone(data))
	if err != nil {
		return err
	}

	tmpName, err := w.writeTemp(next, mode)
	if err != nil {
		// Temp stage failed: the target is untouched, so do not fall back.
		return fmt.Errorf("write temp config for %s: %w", w.path, err)
	}
	renameErr := renameFile(tmpName, w.path)
	if renameErr == nil {
		return nil
	}
	os.Remove(tmpName)
	return w.writeInPlace(next, data, existed, mode, renameErr)
}

// writeTemp writes b to a same-directory temp file and fsyncs it, returning
// its name. The temp file is removed on any failure.
func (w *Writer) writeTemp(b []byte, mode os.FileMode) (name string, err error) {
	tmp, err := os.CreateTemp(filepath.Dir(w.path), filepath.Base(w.path)+".*.tmp")
	if err != nil {
		return "", err
	}
	name = tmp.Name()
	defer func() {
		if err != nil {
			os.Remove(name)
		}
	}()
	if _, err = tmp.Write(b); err != nil {
		tmp.Close()
		return "", err
	}
	if err = tmp.Chmod(mode); err != nil {
		tmp.Close()
		return "", err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return "", err
	}
	if err = tmp.Close(); err != nil {
		return "", err
	}
	return name, nil
}

// writeInPlace is the fallback: back up the old bytes, then truncate and rewrite.
func (w *Writer) writeInPlace(b, old []byte, existed bool, mode os.FileMode, renameErr error) error {
	if existed {
		if err := os.WriteFile(w.path+".bak", old, mode); err != nil {
			return fmt.Errorf("write config backup: %w", errors.Join(err, renameErr))
		}
	}
	f, err := os.OpenFile(w.path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("open config %s: %w", w.path, errors.Join(err, renameErr))
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return fmt.Errorf("write config %s: %w", w.path, errors.Join(err, renameErr))
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync config %s: %w", w.path, errors.Join(err, renameErr))
	}
	return f.Close()
}
