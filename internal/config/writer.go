package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
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

// Update parses the file (a missing or empty file is an empty mapping), calls
// fn, and writes the result. If fn errors the file is left untouched. Comments
// are preserved because the yaml.Node tree is re-encoded.
//
// The write goes to a temp file in the same directory followed by a rename.
// When rename fails (e.g. EBUSY on a single-file Docker bind mount), the
// previous bytes are saved to <path>.bak and the file is rewritten in place.
func (w *Writer) Update(fn func(doc *yaml.Node) error) error {
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

	var doc yaml.Node
	if len(bytes.TrimSpace(data)) > 0 {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("parse config %s: %w", w.path, err)
		}
	}
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode}
	}
	if len(doc.Content) == 0 {
		doc.Content = []*yaml.Node{{Kind: yaml.MappingNode}}
	}

	if err := fn(&doc); err != nil {
		return err
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return fmt.Errorf("encode config %s: %w", w.path, err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("encode config %s: %w", w.path, err)
	}

	renameErr := w.writeViaRename(buf.Bytes(), mode)
	if renameErr == nil {
		return nil
	}
	return w.writeInPlace(buf.Bytes(), data, existed, mode, renameErr)
}

// writeViaRename writes b to a same-directory temp file, fsyncs it, and
// renames it over the target. The temp file is removed on any failure.
func (w *Writer) writeViaRename(b []byte, mode os.FileMode) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(w.path), filepath.Base(w.path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			os.Remove(tmpName)
		}
	}()
	if _, err = tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return renameFile(tmpName, w.path)
}

// writeInPlace is the fallback: back up the old bytes, then truncate and rewrite.
func (w *Writer) writeInPlace(b, old []byte, existed bool, mode os.FileMode, renameErr error) error {
	if existed {
		if err := os.WriteFile(w.path+".bak", old, mode); err != nil {
			return fmt.Errorf("write config backup (after rename failed: %v): %w", renameErr, err)
		}
	}
	f, err := os.OpenFile(w.path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("open config %s (after rename failed: %v): %w", w.path, renameErr, err)
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return fmt.Errorf("write config %s: %w", w.path, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync config %s: %w", w.path, err)
	}
	return f.Close()
}
