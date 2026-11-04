package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"go.yaml.in/yaml/v3"
)

// CredentialsFile is the local-only file in the config directory that holds
// login credentials. It is never part of a portable domain: copy it to carry
// a login to another install, delete it to reopen first-run /auth/setup.
const CredentialsFile = "credentials.yaml"

// ErrAlreadyConfigured is returned by SetupCredentials when credentials exist.
var ErrAlreadyConfigured = errors.New("already configured")

// Credentials are the OSS login credentials.
type Credentials struct {
	Username     string `yaml:"username"`
	PasswordHash string `yaml:"password_hash"`
}

// credMu serializes SaveCredentials calls within the process.
var credMu sync.Mutex

func credentialsPath(dir string) string { return filepath.Join(dir, CredentialsFile) }

// LoadCredentials reads credentials.yaml from dir. The bool is false when the
// file does not exist.
func LoadCredentials(dir string) (Credentials, bool, error) {
	var c Credentials
	data, err := os.ReadFile(credentialsPath(dir))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return c, false, nil
		}
		return c, false, fmt.Errorf("read credentials: %w", err)
	}
	if err := yaml.Unmarshal(data, &c); err != nil {
		return c, false, fmt.Errorf("parse credentials: %w", err)
	}
	return c, true, nil
}

// SaveCredentials writes c to credentials.yaml atomically, creating the file
// with mode 0600 when missing. Comments in an existing file are kept.
func SaveCredentials(dir string, c Credentials) error {
	credMu.Lock()
	defer credMu.Unlock()
	path := credentialsPath(dir)
	// Writer keeps an existing file's mode; force 0600 before (so the rewrite
	// and any fallback .bak are written at 0600) and after.
	if err := chmodIfExists(path); err != nil {
		return err
	}
	err := updateNodes(NewWriterMode(path, 0600), func(doc *yaml.Node) error {
		root := doc.Content[0]
		if root.Kind != yaml.MappingNode {
			return fmt.Errorf("credentials: top level is not a mapping")
		}
		setScalar(root, "username", c.Username)
		setScalar(root, "password_hash", c.PasswordHash)
		return nil
	})
	// The rename-failure fallback leaves <path>.bak with the old hash; an
	// existing .bak keeps its old mode, so tighten both regardless of err.
	if cerr := chmodIfExists(path); err == nil {
		err = cerr
	}
	if cerr := chmodIfExists(path + ".bak"); err == nil {
		err = cerr
	}
	return err
}

func chmodIfExists(p string) error {
	if err := os.Chmod(p, 0600); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("chmod credentials: %w", err)
	}
	return nil
}

// SetupCredentials creates credentials.yaml with c only if it does not exist
// yet. The create is atomic: a fully written temp file is hard-linked into
// place, which fails if the target exists, so of many concurrent callers
// (in this or another process) exactly one succeeds and the rest get
// ErrAlreadyConfigured. Readers never see a partially written file.
func SetupCredentials(dir string, c Credentials) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("encode credentials: %w", err)
	}
	target := credentialsPath(dir)
	tmp, err := os.CreateTemp(dir, CredentialsFile+".*.tmp")
	if err != nil {
		return fmt.Errorf("create credentials: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Chmod(0600)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("write credentials: %w", err)
	}
	linkErr := os.Link(tmpName, target)
	if linkErr == nil {
		return nil
	}
	if errors.Is(linkErr, fs.ErrExist) {
		return ErrAlreadyConfigured
	}
	// Hard links unsupported on this filesystem: fall back to O_EXCL create.
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return ErrAlreadyConfigured
		}
		return fmt.Errorf("create credentials: %w", errors.Join(err, linkErr))
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		// Do not leave a partial file that would block setup forever.
		os.Remove(target)
		return fmt.Errorf("write credentials: %w", err)
	}
	return nil
}

// setScalar sets key to a string scalar in mapping m, replacing any value.
func setScalar(m *yaml.Node, key, val string) {
	v := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: val}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = v
			return
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, v)
}
