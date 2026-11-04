package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"gopkg.in/yaml.v3"
)

// legacyNote marks the planner keys that planner rules replace.
const legacyNote = "# Legacy: replaced by planner rules (Routine, Focus, Transition). Do not extend."

// legacyNoteKeys are the planner.yaml keys that carry legacyNote.
var legacyNoteKeys = map[string]bool{"lunch": true, "focus": true}

// legacyCredentialKeys are the auth keys that left config.yaml: username and
// password_hash move to credentials.yaml, session_token is dropped (it was
// never read).
var legacyCredentialKeys = []string{"username", "password_hash", "session_token"}

// newFileHeaders head the domain files that migration creates.
var newFileHeaders = map[Domain]string{
	DomainPlanner:   "# Planner: working hours, lunch, focus and generated-event colors.\n# Portable: copy with the rest of the config directory.",
	DomainCalendars: "# Calendars: sources (accounts read from) and destinations.\n# Secrets do not belong here: use ics_url_env / credentials_file references.",
}

// movedDomains are the domains whose keys used to live in config.yaml, in the
// order their files are written. planner.yaml is written last because its
// presence is what marks a directory as migrated.
var movedDomains = []Domain{DomainCalendars, DomainPlanner}

// MigrateLegacy converts a legacy single-file config in dir to the directory
// layout. It runs at startup before LoadDir.
//
// When config.yaml holds planner or calendars keys (working_hours, lunch,
// focus, colors, sources, destinations) and planner.yaml is absent, those
// keys move to planner.yaml and calendars.yaml with their comments. When
// config.yaml holds auth.username or auth.password_hash, they move to
// credentials.yaml (created 0600; an existing credentials.yaml is never
// overwritten) and auth.session_token is dropped. The original file is kept
// as config.yaml.bak (0600: it may hold the password hash). Unknown keys stay
// in config.yaml.
//
// It is idempotent: once nothing is left to move it returns false and writes
// nothing. It refuses (error, nothing written) when calendars.yaml already
// exists and config.yaml still holds sources or destinations, since one of
// the two would be lost.
func MigrateLegacy(dir string) (migrated bool, err error) {
	appPath := filepath.Join(dir, DomainApp.File())
	original, err := os.ReadFile(appPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("migrate: read %s: %w", DomainApp.File(), err)
	}
	var probe yaml.Node
	if err := yaml.Unmarshal(original, &probe); err != nil {
		return false, fmt.Errorf("migrate: parse %s: %w", DomainApp.File(), err)
	}
	root := rootMapping(&probe)
	if root == nil {
		return false, nil
	}

	plannerExists, err := exists(filepath.Join(dir, DomainPlanner.File()))
	if err != nil {
		return false, err
	}
	var move map[Domain][]string
	if !plannerExists {
		move = legacyDomainKeys(root)
	}
	creds := hasLegacyCredentials(root)
	if len(move) == 0 && !creds {
		return false, nil
	}
	for _, d := range movedDomains {
		if len(move[d]) == 0 {
			continue
		}
		ok, err := exists(filepath.Join(dir, d.File()))
		if err != nil {
			return false, err
		}
		if ok {
			return false, fmt.Errorf("migrate: %s still holds %v but %s already exists; merge them by hand",
				DomainApp.File(), move[d], d.File())
		}
	}

	var created []string
	cleanup := func() {
		for _, p := range created {
			os.Remove(p)
		}
	}
	// The closure runs on the writer's own parse of config.yaml; returning an
	// error leaves config.yaml untouched.
	err = NewWriter(appPath).Update(func(doc *yaml.Node) error {
		root := rootMapping(doc)
		if root == nil {
			return errors.New("migrate: config.yaml root is not a mapping")
		}
		if creds {
			p, err := moveCredentials(dir, root)
			if err != nil {
				return err
			}
			if p != "" {
				created = append(created, p)
			}
		}
		pairs := extractPairs(doc, root, move)
		for _, d := range movedDomains {
			if len(pairs[d]) == 0 {
				continue
			}
			p := filepath.Join(dir, d.File())
			err := NewWriter(p).Update(func(nd *yaml.Node) error {
				nd.HeadComment = newFileHeaders[d]
				m := nd.Content[0]
				m.Content = append(m.Content, pairs[d]...)
				return nil
			})
			if err != nil {
				return fmt.Errorf("migrate: write %s: %w", d.File(), err)
			}
			created = append(created, p)
		}
		bak := appPath + ".bak"
		if err := os.WriteFile(bak, original, 0600); err != nil {
			return fmt.Errorf("migrate: write backup: %w", err)
		}
		if err := os.Chmod(bak, 0600); err != nil {
			return fmt.Errorf("migrate: chmod backup: %w", err)
		}
		return nil
	})
	if err != nil {
		cleanup()
		return false, err
	}
	return true, nil
}

// LegacyKeys returns the planner and calendars keys still present in dir's
// config.yaml. They are ignored on load (each domain is read only from its
// own file), so startup warns about them. Errors yield nil.
func LegacyKeys(dir string) []string {
	b, err := os.ReadFile(filepath.Join(dir, DomainApp.File()))
	if err != nil {
		return nil
	}
	var doc yaml.Node
	if yaml.Unmarshal(b, &doc) != nil {
		return nil
	}
	root := rootMapping(&doc)
	if root == nil {
		return nil
	}
	var out []string
	for _, d := range []Domain{DomainPlanner, DomainCalendars} {
		out = append(out, legacyDomainKeys(root)[d]...)
	}
	return out
}

func rootMapping(doc *yaml.Node) *yaml.Node {
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	return doc.Content[0]
}

func exists(p string) (bool, error) {
	_, err := os.Stat(p)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("migrate: %w", err)
}

// legacyDomainKeys returns, per moved domain, the domain's keys present in
// the config.yaml mapping root, in domain-key order.
func legacyDomainKeys(root *yaml.Node) map[Domain][]string {
	out := map[Domain][]string{}
	for _, d := range movedDomains {
		for _, dk := range domainKeys[d] {
			if findKey(root, dk.name) >= 0 {
				out[d] = append(out[d], dk.name)
			}
		}
	}
	return out
}

func authMapping(root *yaml.Node) *yaml.Node {
	i := findKey(root, "auth")
	if i < 0 || root.Content[i].Kind != yaml.MappingNode {
		return nil
	}
	return root.Content[i]
}

func hasLegacyCredentials(root *yaml.Node) bool {
	auth := authMapping(root)
	if auth == nil {
		return false
	}
	for _, k := range legacyCredentialKeys {
		if findKey(auth, k) >= 0 {
			return true
		}
	}
	return false
}

// moveCredentials copies auth.username and auth.password_hash to
// credentials.yaml unless that file exists, then removes all
// legacyCredentialKeys from auth (and auth itself if left empty). It returns
// the path it created, if any.
func moveCredentials(dir string, root *yaml.Node) (string, error) {
	auth := authMapping(root)
	scalar := func(k string) string {
		if i := findKey(auth, k); i >= 0 {
			return auth.Content[i].Value
		}
		return ""
	}
	c := Credentials{Username: scalar("username"), PasswordHash: scalar("password_hash")}
	var created string
	if c.PasswordHash != "" {
		switch err := SetupCredentials(dir, c); {
		case err == nil:
			created = credentialsPath(dir)
		case errors.Is(err, ErrAlreadyConfigured):
			// credentials.yaml wins: it is newer than the legacy hash.
		default:
			return "", fmt.Errorf("migrate: %w", err)
		}
	}
	for _, k := range legacyCredentialKeys {
		if i := findKey(auth, k); i >= 0 {
			removePair(auth, i)
		}
	}
	if len(auth.Content) == 0 {
		removePair(root, findKey(root, "auth"))
	}
	return created, nil
}

// extractPairs removes the keys named in move from root and returns their
// key/value node pairs per domain, in file order, with comments. A header
// comment on root's first key stays in config.yaml.
func extractPairs(doc, root *yaml.Node, move map[Domain][]string) map[Domain][]*yaml.Node {
	owner := map[string]Domain{}
	for d, keys := range move {
		for _, k := range keys {
			owner[k] = d
		}
	}
	out := map[Domain][]*yaml.Node{}
	var keep []*yaml.Node
	var header string
	for i := 0; i+1 < len(root.Content); i += 2 {
		k, v := root.Content[i], root.Content[i+1]
		d, ok := owner[k.Value]
		if !ok {
			keep = append(keep, k, v)
			continue
		}
		nk := *k
		if i == 0 {
			header, nk.HeadComment = k.HeadComment, ""
		}
		if legacyNoteKeys[k.Value] {
			if nk.HeadComment != "" {
				nk.HeadComment += "\n"
			}
			nk.HeadComment += legacyNote
		}
		out[d] = append(out[d], &nk, v)
	}
	root.Content = keep
	if header != "" {
		switch {
		case len(keep) > 0:
			keep[0].HeadComment = joinComments(header, keep[0].HeadComment)
		default:
			doc.HeadComment = joinComments(doc.HeadComment, header)
		}
	}
	return out
}

func joinComments(a, b string) string {
	parts := slices.DeleteFunc([]string{a, b}, func(s string) bool { return s == "" })
	if len(parts) == 2 {
		return parts[0] + "\n\n" + parts[1]
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return ""
}
