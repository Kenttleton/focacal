package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// ErrValidation marks a request whose shape is fine but whose values are not:
// a validator failed, a required value was nulled, or a secret was sent
// inline. HTTP handlers map it to 422.
var ErrValidation = errors.New("validation failed")

// ErrBadRequest marks a malformed request: invalid JSON, a body that is not a
// JSON object, unknown keys, or values of the wrong JSON type. HTTP handlers
// map it to 400.
var ErrBadRequest = errors.New("bad request")

// secretFields are never accepted through the API with a non-null value
// (spec 9.5: credentials stay in .env or referenced files). A null is allowed
// so an inline secret written by hand can be removed.
var secretFields = map[string]bool{
	"password":      true,
	"ics_url":       true,
	"url":           true,
	"api_key":       true,
	"password_hash": true,
	"session_token": true,
}

// PatchOptions tunes Patch.
type PatchOptions struct{ DryRun bool }

// Pruned names something a delete removed as a consequence.
type Pruned struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// View is what the API returns for one domain.
type View struct {
	// Values are the saved values (file contents over defaults; env vars are
	// not applied). Collections are objects keyed by item id; secrets are
	// omitted.
	Values map[string]any `json:"values"`
	// Order lists each collection's ids in file order, since JSON objects are
	// unordered.
	Order map[string][]string `json:"order"`
	// PendingRestart is true when the saved server block differs from the
	// one the process started with (Live.RestartRequired). Every other
	// setting applies live.
	PendingRestart bool `json:"pending_restart"`
	// LoadError is the last reload failure (the running config is still the
	// last good one), empty when the files loaded cleanly.
	LoadError string `json:"load_error"`
	// Pruned lists what a delete in this patch removed beyond the items it
	// named.
	Pruned []Pruned `json:"pruned,omitempty"`
}

// Service reads and patches the domain files of one config directory.
// Writes go to disk and then through Live.Reload, which applies them to the
// running config.
type Service struct {
	dir     string
	live    *Live
	writers map[Domain]*Writer
}

// NewService returns a Service over dir. Missing domain files are treated as
// empty. live supplies pending_restart and load_error; it may be nil (both
// then read as unset, and Patch does not reload).
func NewService(dir string, live *Live) (*Service, error) {
	st, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("config dir: %w", err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("config dir %s: not a directory", dir)
	}
	s := &Service{dir: dir, live: live, writers: map[Domain]*Writer{}}
	for _, d := range Domains {
		s.writers[d] = NewWriter(filepath.Join(dir, d.File()))
	}
	return s, nil
}

// LoadDir loads the config from the domain files in dir (missing files are
// fine) over the built-in defaults, without environment overrides, and
// validates it.
func LoadDir(dir string) (*Config, error) {
	files, err := readDomainFiles(dir)
	if err != nil {
		return nil, err
	}
	cfg, err := loadFiles(files)
	if err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// readDomainFiles returns the bytes of each domain file in dir, keyed by
// domain; a missing file has nil bytes.
func readDomainFiles(dir string) (map[Domain][]byte, error) {
	out := map[Domain][]byte{}
	for _, d := range Domains {
		b, err := os.ReadFile(filepath.Join(dir, d.File()))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read %s: %w", d.File(), err)
		}
		out[d] = b
	}
	return out, nil
}

// Get returns the saved values of d.
func (s *Service) Get(d Domain) (View, error) {
	if !d.Valid() {
		return View{}, fmt.Errorf("%w: %q", ErrUnknownDomain, d)
	}
	files, err := readDomainFiles(s.dir)
	if err != nil {
		return View{}, err
	}
	// While the last reload failed (a file that no longer parses or
	// validates), show the last good values with the recorded error rather
	// than the broken file, so a UI can display load_error.
	if s.live != nil && s.live.LoadError() != nil {
		return s.view(d, s.live.fileConfig())
	}
	cfg, err := loadFiles(files)
	if err != nil {
		return View{}, err
	}
	return s.view(d, cfg)
}

// Patch applies an RFC 7396 merge patch (JSON) to d's file. Collections are
// objects keyed by item id, with null deleting an item. The body is checked
// against d's keys, the patched candidate is validated with d's validator,
// and only then is the file written. On any error the file is untouched.
//
// Deleting an account or calendar also prunes what referenced it (see
// pruneCascade) in the same write; View.Pruned lists that. With
// opts.DryRun everything runs except the write and the reload, so the view
// and Pruned show what the patch would do.
func (s *Service) Patch(d Domain, body []byte, opts PatchOptions) (View, error) {
	if !d.Valid() {
		return View{}, fmt.Errorf("%w: %q", ErrUnknownDomain, d)
	}
	patch, err := decodePatch(body)
	if err != nil {
		return View{}, err
	}
	if err := rejectEmptyMapKeys(d, patch); err != nil {
		return View{}, err
	}
	if err := checkPatch(d, patch); err != nil {
		return View{}, err
	}
	if len(patch) == 0 {
		return s.Get(d)
	}
	return s.update(d, opts, func(map[string]any) (map[string]any, error) { return patch, nil })
}

// Create adds one item to the accounts, calendars or destinations collection
// of the calendars domain. body is the item without an id: the server derives
// the id from name (Slug, made unique case-insensitively; "focacal" is
// reserved among calendars). The request then takes the same validate-then-
// write path as Patch.
func (s *Service) Create(coll string, body []byte) (string, View, error) {
	d := DomainCalendars
	var dk *domainKey
	for i, k := range domainKeys[d] {
		if k.name == coll && k.collection != nil {
			dk = &domainKeys[d][i]
		}
	}
	if dk == nil {
		return "", View{}, badRequestf("cannot create items in %q", coll)
	}
	item, err := decodePatch(body)
	if err != nil {
		return "", View{}, err
	}
	if _, has := item["id"]; has {
		return "", View{}, badRequestf("id is assigned by the server; send name instead")
	}
	name, isStr := item["name"].(string)
	if item["name"] != nil && !isStr {
		return "", View{}, badRequestf("name: must be a string")
	}
	if strings.TrimSpace(name) == "" {
		return "", View{}, validationf("%s: name is required", coll)
	}
	var id string
	v, err := s.update(d, PatchOptions{}, func(doc map[string]any) (map[string]any, error) {
		taken := map[string]bool{}
		for existing := range collectionIDs(doc, d)[coll] {
			taken[strings.ToLower(existing)] = true
		}
		id = UniqueID(name, func(c string) bool {
			return taken[c] || (coll == "calendars" && c == "focacal")
		})
		patch := map[string]any{coll: map[string]any{id: item}}
		if err := checkPatch(d, patch); err != nil {
			return nil, err
		}
		return patch, nil
	})
	if err != nil {
		return "", View{}, err
	}
	return id, v, nil
}

// errDryRun aborts Writer.Update after validation so nothing is written.
var errDryRun = errors.New("dry run")

// update is the shared write path of Patch and Create. prep receives the
// current document (under the writer's lock) and returns the checked merge
// patch to apply.
func (s *Service) update(d Domain, opts PatchOptions, prep func(doc map[string]any) (map[string]any, error)) (View, error) {
	var saved *Config
	var pruned []Pruned
	// The candidate is built inside Update, under the writer's lock, from the
	// bytes that will be replaced, so a concurrent patch cannot slip in
	// between validation and write. Returning an error leaves the file alone.
	err := s.writers[d].Update(func(cur []byte) ([]byte, error) {
		doc := map[string]any{}
		if len(bytes.TrimSpace(cur)) > 0 {
			if err := yaml.Unmarshal(cur, &doc); err != nil {
				return nil, fmt.Errorf("parse %s: top level must be a mapping: %w", d.File(), err)
			}
			if doc == nil {
				doc = map[string]any{} // comments only
			}
		}
		patch, err := prep(doc)
		if err != nil {
			return nil, err
		}
		before := collectionIDs(doc, d)
		if err := ApplyMergePatch(doc, patch, d.collections()); err != nil {
			return nil, fmt.Errorf("apply patch to %s: %w", d.File(), err)
		}
		if d == DomainCalendars {
			after := collectionIDs(doc, d)
			deleted := map[string][]string{}
			for _, coll := range []string{"accounts", "calendars"} {
				for id := range before[coll] {
					if !after[coll][id] {
						deleted[coll] = append(deleted[coll], id)
					}
				}
				slices.Sort(deleted[coll])
			}
			pruned = pruneCascade(doc, deleted)
		}
		if err := checkNewIDs(doc, before, d); err != nil {
			return nil, err
		}
		patched, err := yaml.Marshal(doc)
		if err != nil {
			return nil, fmt.Errorf("encode %s: %w", d.File(), err)
		}
		files, err := readDomainFiles(s.dir)
		if err != nil {
			return nil, err
		}
		files[d] = patched
		cfg, err := loadFiles(files)
		if err != nil {
			// The patch was shape-checked (checkPatch) and its map keys
			// checked (rejectEmptyMapKeys), so a failure here is expected
			// to come from a file on disk (a hand edit that no longer
			// parses or decodes). That is not the request's fault, so it
			// carries no error kind (HTTP 500). If a patch-caused case is
			// found, reject it in checkPatch or rejectEmptyMapKeys rather
			// than tagging this error.
			return nil, err
		}
		var verr error
		if d == DomainCalendars {
			// Dangling references the file already had are not this
			// request's fault: reject only the new ones.
			verr = cfg.ValidateCalendarsAllowing(s.existingDangling(cur))
		} else {
			verr = d.validate(cfg)
		}
		if verr != nil {
			return nil, fmt.Errorf("%w: %w", ErrValidation, verr)
		}
		out, err := marshalDomain(d, cfg)
		if err != nil {
			return nil, err
		}
		saved = cfg
		if opts.DryRun {
			return nil, errDryRun
		}
		return out, nil
	})
	if err != nil && !errors.Is(err, errDryRun) {
		return View{}, err
	}
	// The returned view is the saved candidate, not the running config.
	// Apply the write now so the file watcher finds nothing left to do. A
	// failure here does not fail the request: the file is already written
	// and valid for d, so the error means something outside this request
	// (env, another domain, cross-domain validation) blocks the reload. It
	// is logged by Live and reported through View.LoadError.
	if s.live != nil && !opts.DryRun {
		_, _ = s.live.Reload()
	}
	v, err := s.view(d, saved)
	if err != nil {
		return View{}, err
	}
	v.Pruned = pruned
	return v, nil
}

// existingDangling lists the dangling references of the calendars file as it
// is before a patch (cur), with the other domains as on disk. A file that
// does not load yields none, so everything stays strict.
func (s *Service) existingDangling(cur []byte) []string {
	files, err := readDomainFiles(s.dir)
	if err != nil {
		return nil
	}
	files[DomainCalendars] = cur
	cfg, err := loadFiles(files)
	if err != nil {
		return nil
	}
	return cfg.DanglingRefs()
}

// collectionIDs returns the ids of every collection of d present in doc.
func collectionIDs(doc map[string]any, d Domain) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for name := range d.collections() {
		ids := map[string]bool{}
		list, _ := doc[name].([]any)
		for _, it := range list {
			if m, ok := it.(map[string]any); ok {
				ids[str(m["id"])] = true
			}
		}
		out[name] = ids
	}
	return out
}

// checkNewIDs rejects an id the patch created that does not match ValidID.
// Ids already in the file keep loading and stay editable even when they do
// not match (spec section 5), so only new ids are checked.
func checkNewIDs(doc map[string]any, before map[string]map[string]bool, d Domain) error {
	for name, was := range before {
		list, _ := doc[name].([]any)
		for _, it := range list {
			m, ok := it.(map[string]any)
			if !ok {
				continue
			}
			if id := str(m["id"]); !was[id] && !ValidID(id) {
				return validationf("%s: id %q is invalid: use lowercase letters, digits and dashes, up to 63 characters", name, id)
			}
		}
	}
	return nil
}

// rejectEmptyMapKeys refuses an empty key in map-typed values (colors): it
// names nothing, so it could never be addressed again.
func rejectEmptyMapKeys(d Domain, patch map[string]any) error {
	for _, dk := range domainKeys[d] {
		if dk.typ.Kind() != reflect.Map {
			continue
		}
		if m, ok := patch[dk.name].(map[string]any); ok {
			if _, empty := m[""]; empty {
				return badRequestf("%s: an empty key is not allowed", dk.name)
			}
		}
	}
	return nil
}

// projection returns d's values in c in a comparable form: JSON-decoded,
// with empty lists and objects folded to nil, so an added-then-deleted item
// (empty list on disk vs absent at start) compares equal.
func projection(c *Config, d Domain) any { return projectionOf(c, d, nil) }

// projectionOf is projection restricted to the keys named in only (all keys
// when only is empty).
func projectionOf(c *Config, d Domain, only []string) any {
	var out []any
	for _, dk := range domainKeys[d] {
		if len(only) > 0 && !slices.Contains(only, dk.name) {
			continue
		}
		v, err := jsonValue(dk.get(c))
		if err != nil {
			v = dk.get(c)
		}
		out = append(out, foldEmpty(v))
	}
	return out
}

func foldEmpty(v any) any {
	switch t := v.(type) {
	case []any:
		if len(t) == 0 {
			return nil
		}
		for i := range t {
			t[i] = foldEmpty(t[i])
		}
	case map[string]any:
		if len(t) == 0 {
			return nil
		}
		for k := range t {
			t[k] = foldEmpty(t[k])
		}
	}
	return v
}

func (s *Service) view(d Domain, cfg *Config) (View, error) {
	v := View{Values: map[string]any{}, Order: map[string][]string{}}
	for _, dk := range domainKeys[d] {
		var err error
		switch dk.name {
		case "accounts":
			v.Values[dk.name], v.Order[dk.name], err = collectionView(cfg.Accounts, func(a AccountConfig) string { return a.ID },
				func(m map[string]any, a AccountConfig) {
					delete(m, "password")
					redactURLFields(m)
					m["has_secret"] = accountHasSecret(a)
				})
		case "calendars":
			v.Values[dk.name], v.Order[dk.name], err = collectionView(cfg.Calendars, func(c CalendarConfig) string { return c.ID },
				func(m map[string]any, c CalendarConfig) {
					delete(m, "url")
					m["has_secret"] = calendarHasSecret(c)
				})
		case "destinations":
			v.Values[dk.name], v.Order[dk.name], err = collectionView(cfg.Destinations, func(d DestinationConfig) string { return d.ID }, nil)
		default:
			v.Values[dk.name], err = jsonValue(dk.get(cfg))
			if v.Values[dk.name] == nil && dk.typ.Kind() == reflect.Map {
				v.Values[dk.name] = map[string]any{}
			}
		}
		if err != nil {
			return View{}, err
		}
	}
	if s.live != nil {
		v.PendingRestart = s.live.RestartRequired()
		if err := s.live.LoadError(); err != nil {
			v.LoadError = err.Error()
		}
	}
	return v, nil
}

// jsonValue converts v to its JSON-decoded form (maps, slices, float64...).
func jsonValue(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func jsonObject(v any) (map[string]any, error) {
	out, err := jsonValue(v)
	if err != nil {
		return nil, err
	}
	m, _ := out.(map[string]any)
	return m, nil
}

// accountHasSecret reports whether an account has a credential configured,
// inline or by reference: an inline password, a credentials_file (OAuth
// client credentials) or userinfo in caldav_url. token_file is runtime state
// and does not count.
func accountHasSecret(a AccountConfig) bool {
	return a.Password != "" || a.CredentialsFile != "" || hasUserinfo(a.CalDAVURL)
}

// calendarHasSecret reports whether a calendar is an ICS feed, whose URL
// (inline or by env reference) is a secret.
func calendarHasSecret(c CalendarConfig) bool { return c.URL != "" || c.URLEnv != "" }

// stripUserinfo removes a "user:password@" part from a URL's authority and
// reports whether there was one. It works on the raw string so a URL that
// net/url rejects is still handled.
func stripUserinfo(raw string) (string, bool) {
	i := strings.Index(raw, "://")
	if i < 0 {
		return raw, false
	}
	rest := raw[i+3:]
	end := strings.IndexAny(rest, "/?#")
	if end < 0 {
		end = len(rest)
	}
	at := strings.LastIndex(rest[:end], "@")
	if at < 0 {
		return raw, false
	}
	return raw[:i+3] + rest[at+1:], true
}

// redactURLFields strips userinfo from the caldav_url of a view item.
func redactURLFields(m map[string]any) {
	if u, ok := m["caldav_url"].(string); ok {
		m["caldav_url"], _ = stripUserinfo(u)
	}
}

func hasUserinfo(raw string) bool { _, ok := stripUserinfo(raw); return ok }

// collectionView renders list as an object keyed by id plus the ids in file
// order. scrub, when set, removes secrets from each item's JSON object.
func collectionView[T any](list []T, id func(T) string, scrub func(map[string]any, T)) (map[string]any, []string, error) {
	values := map[string]any{}
	order := []string{}
	for _, it := range list {
		m, err := jsonObject(it)
		if err != nil {
			return nil, nil, err
		}
		if scrub != nil {
			scrub(m, it)
		}
		values[id(it)] = m
		order = append(order, id(it))
	}
	return values, order, nil
}

func badRequestf(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrBadRequest, fmt.Sprintf(format, a...))
}

func validationf(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrValidation, fmt.Sprintf(format, a...))
}

// decodePatch parses body as exactly one JSON object.
func decodePatch(body []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	var v any
	if err := dec.Decode(&v); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, badRequestf("empty body")
		}
		return nil, badRequestf("malformed JSON: %v", err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, badRequestf("unexpected data after the JSON object")
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, badRequestf("body must be a JSON object")
	}
	return m, nil
}

// checkPatch checks patch against d's allowed keys and value shapes. Unknown
// keys and wrong JSON types are ErrBadRequest; nulled required values,
// inline secrets and ids that do not match their key are ErrValidation.
func checkPatch(d Domain, patch map[string]any) error {
	keys := map[string]domainKey{}
	for _, dk := range domainKeys[d] {
		keys[dk.name] = dk
	}
	for _, k := range sortedKeys(patch) {
		dk, ok := keys[k]
		if !ok {
			return badRequestf("unknown key %q for domain %s", k, d)
		}
		v := patch[k]
		if dk.collection == nil {
			if err := checkValue(k, v, dk.typ, false); err != nil {
				return err
			}
			continue
		}
		if v == nil {
			continue // removes the whole list; the validator decides
		}
		entries, ok := v.(map[string]any)
		if !ok {
			return badRequestf("%s: must be an object keyed by id", k)
		}
		for _, id := range sortedKeys(entries) {
			if err := checkItem(k, id, entries[id], dk); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkItem(coll, id string, e any, dk domainKey) error {
	path := coll + "." + id
	spec := dk.collection
	identity := spec.Identity(id)
	// A null deletes the item. It comes first so an existing id that
	// would not be accepted for a new item (hand-written, with ":" or "|")
	// can still be deleted. Creating such an id stays rejected: by the
	// shape checks below and by ValidID in checkNewIDs.
	if e == nil {
		return nil
	}
	if id == "" || spec.Key(identity) != id {
		return validationf("%s: invalid id %q", coll, id)
	}
	// "|" and ":" are separators in runtime keys (sync_state rows are
	// "<id>:<calendar>"), so they cannot appear in a new id.
	for _, f := range sortedKeys(identity) {
		if strings.ContainsAny(str(identity[f]), "|:") {
			return validationf("%s: %s must not contain \"|\" or \":\"", path, f)
		}
	}
	item, ok := e.(map[string]any)
	if !ok {
		return badRequestf("%s: must be an object or null", path)
	}
	if err := checkValue(path, item, dk.typ, true); err != nil {
		return err
	}
	for _, f := range sortedKeys(identity) {
		if got, present := item[f]; present && got != identity[f] {
			return validationf("%s.%s: %v must match the item key", path, f, got)
		}
	}
	return nil
}

// checkValue checks that v (JSON-decoded) fits Go type t. inItem relaxes the
// null rule inside collection items, where null clears an optional field.
func checkValue(path string, v any, t reflect.Type, inItem bool) error {
	if v == nil {
		switch t.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Map:
			return nil
		}
		if inItem {
			return nil
		}
		return validationf("%s: null is not allowed, a value is required", path)
	}
	if t == reflect.TypeFor[InputList]() {
		switch iv := v.(type) {
		case string:
			if iv != "all" {
				return badRequestf("%s: must be \"all\" or a list", path)
			}
			return nil
		case []any:
			return checkValue(path, v, reflect.TypeFor[[]DestinationInput](), inItem)
		}
		return badRequestf("%s: must be \"all\" or a list", path)
	}
	switch t.Kind() {
	case reflect.Pointer:
		return checkValue(path, v, t.Elem(), inItem)
	case reflect.String:
		if _, ok := v.(string); !ok {
			return badRequestf("%s: must be a string", path)
		}
	case reflect.Bool:
		if _, ok := v.(bool); !ok {
			return badRequestf("%s: must be a boolean", path)
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		f, ok := v.(float64)
		if !ok || f != math.Trunc(f) || math.Abs(f) > 1<<53 {
			return badRequestf("%s: must be an integer", path)
		}
	case reflect.Slice:
		arr, ok := v.([]any)
		if !ok {
			return badRequestf("%s: must be a list", path)
		}
		for i, e := range arr {
			p := fmt.Sprintf("%s[%d]", path, i)
			if e == nil {
				return badRequestf("%s: list items must not be null", p)
			}
			if err := checkValue(p, e, t.Elem(), inItem); err != nil {
				return err
			}
		}
	case reflect.Map:
		m, ok := v.(map[string]any)
		if !ok {
			return badRequestf("%s: must be an object", path)
		}
		for _, k := range sortedKeys(m) {
			if m[k] == nil {
				continue // deletes the entry
			}
			if err := checkValue(path+"."+k, m[k], t.Elem(), inItem); err != nil {
				return err
			}
		}
	case reflect.Struct:
		m, ok := v.(map[string]any)
		if !ok {
			return badRequestf("%s: must be an object", path)
		}
		for _, k := range sortedKeys(m) {
			p := path + "." + k
			if secretFields[k] && m[k] != nil {
				return validationf("%s: secrets cannot be set through the API; use an env var or file reference", p)
			}
			ft, ok := fieldByJSONName(t, k)
			if !ok {
				return badRequestf("%s: unknown field", p)
			}
			if err := checkValue(p, m[k], ft, inItem); err != nil {
				return err
			}
			if err := checkReference(p, k, m[k]); err != nil {
				return err
			}
		}
	default:
		return badRequestf("%s: unsupported field type %s", path, t)
	}
	return nil
}

// envRefPattern limits API-set env references to Focacal's own namespace, so
// the API cannot point a source at an arbitrary process variable.
var envRefPattern = regexp.MustCompile(`^FOCACAL_[A-Z0-9_]+$`)

// fileRefFields are file references set through the API. They must be
// relative (resolved under the config dir) with no ".." segment, so the API
// cannot aim a token write at an arbitrary file such as the DB.
var fileRefFields = map[string]bool{"credentials_file": true, "token_file": true}

// checkReference applies the API-only rules for *_env and file reference
// fields (R12). Hand-written files and startup validation are not affected.
func checkReference(path, field string, v any) error {
	s, ok := v.(string)
	if !ok || s == "" {
		return nil // null or empty clears the reference
	}
	if field == "caldav_url" {
		if _, userinfo := stripUserinfo(s); userinfo {
			return validationf("%s: must not embed credentials (user:password@); set username, and keep the password out of the API", path)
		}
	}
	if strings.HasSuffix(field, "_env") && !envRefPattern.MatchString(s) {
		return validationf("%s: env var name must match %s", path, envRefPattern)
	}
	if fileRefFields[field] {
		if filepath.IsAbs(s) || strings.HasPrefix(s, "/") || strings.HasPrefix(s, `\`) || filepath.VolumeName(s) != "" {
			return validationf("%s: must be a path relative to the config directory", path)
		}
		for _, seg := range strings.FieldsFunc(s, func(r rune) bool { return r == '/' || r == '\\' }) {
			if seg == ".." {
				return validationf("%s: must not contain \"..\"", path)
			}
		}
	}
	return nil
}

func fieldByJSONName(t reflect.Type, name string) (reflect.Type, bool) {
	for i := range t.NumField() {
		f := t.Field(i)
		tag, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if tag == name {
			return f.Type, true
		}
	}
	return nil, false
}
