package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

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
	DomainPlanner:   "# Planner: working hours, lunch, focus, scheduler agent and generated-event colors.\n# Portable: copy with the rest of the config directory.",
	DomainCalendars: "# Calendars: sources (accounts read from), destinations and the OAuth redirect.\n# Secrets do not belong here: use ics_url_env / credentials_file references.",
}

// movedDomains are the domains whose keys used to live in config.yaml, in
// the order their files are written.
var movedDomains = []Domain{DomainPlanner, DomainCalendars}

// MigrateLegacy converts a legacy single-file config in dir to the directory
// layout. It runs at startup before LoadDir.
//
// When config.yaml holds planner or calendars keys (working_hours, lunch,
// focus, colors, agent, sources, destinations, oauth), those keys move to planner.yaml and
// calendars.yaml with their comments. When config.yaml holds auth.username or
// auth.password_hash, they move to credentials.yaml (created 0600; an
// existing credentials.yaml is never overwritten) and auth.session_token is
// dropped. The original file is kept as config.yaml.bak (0600: it may hold
// the password hash). Unknown keys stay in config.yaml.
//
// A target file that already exists receives the moved keys it lacks (the
// shipped samples leave agent and oauth out or commented). A moved key it
// already holds must have the same value (a rerun after a crash); otherwise
// MigrateLegacy returns an error and changes nothing, because one of the two
// copies would be lost.
// The migrated result is loaded and validated, and compared with what the
// single file meant, before anything is written.
//
// Files are written credentials, planner.yaml, calendars.yaml, the backup,
// then config.yaml. A failure before config.yaml is rewritten removes what
// this run created. Once the rewrite has started nothing is removed: the
// in-place fallback may already have changed config.yaml, and the next start
// resumes from the files left behind.
//
// It is idempotent: once nothing is left to move it returns false and writes
// nothing.
func MigrateLegacy(dir string) (migrated bool, err error) {
	appPath := filepath.Join(dir, DomainApp.File())
	original, err := os.ReadFile(appPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("migrate: read %s: %w", DomainApp.File(), err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(original, &doc); err != nil {
		return false, fmt.Errorf("migrate: parse %s: %w", DomainApp.File(), err)
	}
	root := rootMapping(&doc)
	if root == nil {
		return false, nil
	}
	move := legacyDomainKeys(root)
	creds := hasLegacyCredentials(root)
	if len(move) == 0 && !creds {
		return false, nil
	}

	// Build every output from this one parse.
	var c Credentials
	if creds {
		c = stripCredentials(root)
	}
	pairs := extractPairs(&doc, root, move)
	out := map[Domain][]byte{}
	for _, d := range movedDomains {
		if len(pairs[d]) == 0 {
			continue
		}
		nd := &yaml.Node{Kind: yaml.DocumentNode, HeadComment: newFileHeaders[d],
			Content: []*yaml.Node{{Kind: yaml.MappingNode, Content: pairs[d]}}}
		if out[d], err = encodeYAML(nd); err != nil {
			return false, fmt.Errorf("migrate: encode %s: %w", d.File(), err)
		}
	}
	if out[DomainApp], err = encodeYAML(&doc); err != nil {
		return false, fmt.Errorf("migrate: encode %s: %w", DomainApp.File(), err)
	}

	// A target that exists gets the moved keys it lacks merged in; a key it
	// already holds must have the same value, or one of the two copies would
	// be lost.
	write := map[Domain]bool{}
	merge := map[Domain]bool{}
	var conflicts []string
	for _, d := range movedDomains {
		if out[d] == nil {
			continue
		}
		existing, err := os.ReadFile(filepath.Join(dir, d.File()))
		switch {
		case os.IsNotExist(err):
			write[d] = true
		case err != nil:
			return false, fmt.Errorf("migrate: read %s: %w", d.File(), err)
		default:
			merged, changed, ok, err := mergeMoved(existing, pairs[d])
			switch {
			case err != nil:
				return false, fmt.Errorf("migrate: parse %s: %w", d.File(), err)
			case !ok:
				conflicts = append(conflicts, fmt.Sprintf("%s (for %s)", d.File(), strings.Join(move[d], ", ")))
			case changed:
				out[d], merge[d] = merged, true
			default:
				out[d] = existing
			}
		}
	}
	if len(conflicts) > 0 {
		return false, fmt.Errorf("migrate: %s in %s is a legacy single-file config, but %s already exist(s); nothing was changed. "+
			"If those are the sample files shipped in the repo, delete them and restart to migrate; "+
			"otherwise merge the keys into them by hand and remove the keys from %s",
			DomainApp.File(), dir, strings.Join(conflicts, " and "), DomainApp.File())
	}

	if err := checkMigrated(dir, original, out, move); err != nil {
		return false, err
	}

	var created []string
	fail := func(err error) (bool, error) {
		for _, p := range created {
			os.Remove(p)
		}
		return false, err
	}
	if creds && c.PasswordHash != "" {
		switch err := SetupCredentials(dir, c); {
		case err == nil:
			created = append(created, credentialsPath(dir))
		case errors.Is(err, ErrAlreadyConfigured):
			// credentials.yaml wins: it is newer than the legacy hash.
		default:
			return fail(fmt.Errorf("migrate: %w", err))
		}
	}
	for _, d := range movedDomains {
		if !write[d] {
			continue
		}
		p := filepath.Join(dir, d.File())
		if err := writeNew(p, out[d]); err != nil {
			return fail(fmt.Errorf("migrate: write %s: %w", d.File(), err))
		}
		created = append(created, p)
	}
	// Merges into existing target files happen after the backup (below).
	bak := appPath + ".bak"
	if err := os.WriteFile(bak, original, 0600); err != nil {
		return fail(fmt.Errorf("migrate: write backup: %w", err))
	}
	if err := os.Chmod(bak, 0600); err != nil {
		return fail(fmt.Errorf("migrate: chmod backup: %w", err))
	}
	// From here on nothing is removed (see the doc comment).
	for _, d := range movedDomains {
		if !merge[d] {
			continue
		}
		b := out[d]
		err := NewWriter(filepath.Join(dir, d.File())).Update(func(doc *yaml.Node) error {
			var merged yaml.Node
			if err := yaml.Unmarshal(b, &merged); err != nil {
				return err
			}
			*doc = merged
			return nil
		})
		if err != nil {
			return false, fmt.Errorf("migrate: merge into %s (the next start resumes): %w", d.File(), err)
		}
	}
	err = NewWriter(appPath).Update(func(d *yaml.Node) error {
		var trimmed yaml.Node
		if err := yaml.Unmarshal(out[DomainApp], &trimmed); err != nil {
			return err
		}
		if trimmed.Kind == 0 {
			trimmed = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
		}
		*d = trimmed
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("migrate: rewrite %s (credentials and new files kept; the next start resumes): %w",
			DomainApp.File(), err)
	}
	return true, nil
}

// mergeMoved adds the key/value pairs of moved that existing lacks. ok is
// false when existing holds one of the keys with a different value (or is not
// a mapping); changed is false when nothing was missing.
func mergeMoved(existing []byte, moved []*yaml.Node) (merged []byte, changed, ok bool, err error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(existing, &doc); err != nil {
		return nil, false, false, err
	}
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	root := rootMapping(&doc)
	if root == nil {
		return nil, false, false, nil
	}
	for i := 0; i+1 < len(moved); i += 2 {
		k, v := moved[i], moved[i+1]
		if j := findKey(root, k.Value); j >= 0 {
			var a, b any
			if root.Content[j].Decode(&a) != nil || v.Decode(&b) != nil || !reflect.DeepEqual(a, b) {
				return nil, false, false, nil
			}
			continue
		}
		root.Content = append(root.Content, k, v)
		changed = true
	}
	if !changed {
		return existing, false, true, nil
	}
	merged, err = encodeYAML(&doc)
	return merged, true, err == nil, err
}

// checkMigrated loads the would-be directory (out over the files already in
// dir) and checks it parses, validates, and means what the single file meant
// for App and every moved domain.
func checkMigrated(dir string, original []byte, out map[Domain][]byte, move map[Domain][]string) error {
	files, err := readDomainFiles(dir)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	for d, b := range out {
		files[d] = b
	}
	got, err := loadFiles(files)
	if err != nil {
		return fmt.Errorf("migrate: migrated files do not load (YAML anchors/aliases across sections are not supported; expand them by hand): %w", err)
	}
	if err := got.Validate(); err != nil {
		return fmt.Errorf("migrate: migrated config is invalid: %w", err)
	}
	want, err := loadFiles(map[Domain][]byte{DomainApp: original, DomainPlanner: original, DomainCalendars: original})
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	check := []Domain{DomainApp}
	for _, d := range movedDomains {
		if len(move[d]) > 0 {
			check = append(check, d)
		}
	}
	for _, d := range check {
		// Only the moved keys: an existing target file's other values are
		// not part of this migration.
		if !reflect.DeepEqual(projectionOf(got, d, move[d]), projectionOf(want, d, move[d])) {
			return fmt.Errorf("migrate: migrated %s would change values; nothing was changed", d.File())
		}
	}
	return nil
}

// encodeYAML encodes doc the way Writer does.
func encodeYAML(doc *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// writeNew creates p with b (0644), failing if p exists.
func writeNew(p string, b []byte) error {
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(p)
	}
	return err
}

// LegacyKeys returns the planner and calendars keys (including agent and
// oauth) still present in dir's config.yaml. They are ignored on load (each domain is read only from its
// own file); MigrateLegacy moves them or refuses, so after it succeeds this
// is normally empty. Errors yield nil.
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

// stripCredentials returns auth.username and auth.password_hash and removes
// all legacyCredentialKeys from auth (and auth itself if left empty).
func stripCredentials(root *yaml.Node) Credentials {
	auth := authMapping(root)
	scalar := func(k string) string {
		if i := findKey(auth, k); i >= 0 {
			return auth.Content[i].Value
		}
		return ""
	}
	c := Credentials{Username: scalar("username"), PasswordHash: scalar("password_hash")}
	for _, k := range legacyCredentialKeys {
		if i := findKey(auth, k); i >= 0 {
			removePair(auth, i)
		}
	}
	if len(auth.Content) == 0 {
		removePair(root, findKey(root, "auth"))
	}
	return c
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
