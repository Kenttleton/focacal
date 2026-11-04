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

	"github.com/mitchellh/mapstructure"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
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
	"api_key":       true,
	"password_hash": true,
	"session_token": true,
}

// View is what the API returns for one domain.
type View struct {
	// Values are the saved values (file contents over defaults; env vars are
	// not applied). Collections are objects keyed by item id; secrets are
	// omitted.
	Values map[string]any `json:"values"`
	// Order lists each collection's ids in file order, since JSON objects are
	// unordered. Destination ids are "id|calendar_id".
	Order map[string][]string `json:"order"`
	// PendingRestart is true when the saved values differ from the ones the
	// process started with.
	PendingRestart bool `json:"pending_restart"`
}

// Service reads and patches the domain files of one config directory.
// Changes are written to disk only; the running config is not mutated.
type Service struct {
	dir     string
	started *Config
	writers map[Domain]*Writer
}

// NewService returns a Service over dir. Missing domain files are treated as
// empty. started must be the env-free snapshot LoadStart returns as started
// (file values over defaults, no environment overrides), not the running
// config: Service compares it with the env-free files for pending_restart, so
// env-overridden values would otherwise read as pending forever. It is never
// modified. A nil started disables the flag.
func NewService(dir string, started *Config) (*Service, error) {
	st, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("config dir: %w", err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("config dir %s: not a directory", dir)
	}
	s := &Service{dir: dir, started: started, writers: map[Domain]*Writer{}}
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

// loadFiles merges the domain files over the defaults and unmarshals them,
// without validating. Each file contributes only its own domain's top-level
// keys; anything else in it (a stray `sources:` in config.yaml, unread blocks
// such as `ollama:`) is ignored, so one file can never override another.
func loadFiles(files map[Domain][]byte) (*Config, error) { return loadFilesWith(files, nil) }

// loadFilesWith is loadFiles with an optional hook to configure the viper
// instance (LoadStart uses it to add environment overrides).
func loadFilesWith(files map[Domain][]byte, prep func(*viper.Viper)) (*Config, error) {
	v := viper.New()
	applyDefaults(v)
	if prep != nil {
		prep(v)
	}
	v.SetConfigType("yaml")
	for _, d := range Domains {
		b := files[d]
		if len(bytes.TrimSpace(b)) == 0 {
			continue
		}
		var all map[string]any
		if err := yaml.Unmarshal(b, &all); err != nil {
			return nil, fmt.Errorf("parse %s: %w", d.File(), err)
		}
		own := map[string]any{}
		for _, dk := range domainKeys[d] {
			if val, ok := all[dk.name]; ok {
				own[dk.name] = val
			}
		}
		if len(own) == 0 {
			continue
		}
		filtered, err := yaml.Marshal(own)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", d.File(), err)
		}
		if err := v.MergeConfig(bytes.NewReader(filtered)); err != nil {
			return nil, fmt.Errorf("parse %s: %w", d.File(), err)
		}
	}
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", redactDecodeError(err))
	}
	return &cfg, nil
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
func (s *Service) Patch(d Domain, body []byte) (View, error) {
	if !d.Valid() {
		return View{}, fmt.Errorf("%w: %q", ErrUnknownDomain, d)
	}
	patch, err := decodePatch(body)
	if err != nil {
		return View{}, err
	}
	if err := lowerMapKeys(d, patch); err != nil {
		return View{}, err
	}
	if err := checkPatch(d, patch); err != nil {
		return View{}, err
	}
	if len(patch) == 0 {
		return s.Get(d)
	}

	var saved *Config
	// The candidate is built inside Update, under the writer's lock, from the
	// same document that will be written, so a concurrent patch cannot slip
	// in between validation and write. Returning an error discards doc.
	err = s.writers[d].Update(func(doc *yaml.Node) error {
		if err := ApplyMergePatch(doc, patch, d.collections()); err != nil {
			return fmt.Errorf("apply patch to %s: %w", d.File(), err)
		}
		patched, err := yaml.Marshal(doc)
		if err != nil {
			return fmt.Errorf("encode %s: %w", d.File(), err)
		}
		files, err := readDomainFiles(s.dir)
		if err != nil {
			return err
		}
		files[d] = patched
		cfg, err := loadFiles(files)
		if err != nil {
			// The patch was shape-checked (checkPatch) and its map keys
			// restricted to ones viper reads back (lowerMapKeys), so a
			// failure here is expected to come from a file on disk (a hand
			// edit that no longer parses or decodes). That is not the
			// request's fault, so it carries no error kind (HTTP 500). If a
			// patch-caused case is found, reject it in checkPatch or
			// lowerMapKeys rather than tagging this error.
			return err
		}
		if err := d.validate(cfg); err != nil {
			return fmt.Errorf("%w: %w", ErrValidation, err)
		}
		saved = cfg
		return nil
	})
	if err != nil {
		return View{}, err
	}
	return s.view(d, saved)
}

// lowerMapKeys lowercases the keys of map-typed values (colors) in patch,
// because viper lowercases map keys on load: writing "Meeting" would read
// back as "meeting", and a later null-delete of "meeting" would not find it.
// It also rejects keys viper cannot read back: "" and any key containing "."
// (viper treats "." as a path separator, so "a.b" would load as a nested map
// and fail to decode).
func lowerMapKeys(d Domain, patch map[string]any) error {
	for _, dk := range domainKeys[d] {
		if dk.typ.Kind() != reflect.Map {
			continue
		}
		m, ok := patch[dk.name].(map[string]any)
		if !ok {
			continue
		}
		lowered := make(map[string]any, len(m))
		for _, k := range sortedKeys(m) {
			v := m[k]
			if k == "" || strings.Contains(k, ".") {
				return badRequestf("%s: key %q is not allowed (keys must be non-empty and must not contain \".\")", dk.name, k)
			}
			lk := strings.ToLower(k)
			if _, dup := lowered[lk]; dup {
				return badRequestf("%s: keys %q collide (keys are case-insensitive)", dk.name, lk)
			}
			lowered[lk] = v
		}
		patch[dk.name] = lowered
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
		case "sources":
			v.Values[dk.name], v.Order[dk.name], err = sourcesView(cfg.Sources)
		case "destinations":
			v.Values[dk.name], v.Order[dk.name], err = destinationsView(cfg.Destinations)
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
	if s.started != nil {
		v.PendingRestart = !reflect.DeepEqual(projection(cfg, d), projection(s.started, d))
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

// hasSecret reports whether a source has a credential configured, inline or
// by reference: an inline password or ics_url, an ics_url_env reference, or
// a credentials_file (OAuth client credentials). token_file is runtime state
// and does not count.
func hasSecret(s SourceConfig) bool {
	_, userinfo := stripUserinfo(s.CalDAVURL)
	return s.Password != "" || s.ICSURL != "" || s.ICSURLEnv != "" || s.CredentialsFile != "" || userinfo
}

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

func sourcesView(list []SourceConfig) (map[string]any, []string, error) {
	values := map[string]any{}
	order := []string{}
	for _, src := range list {
		m, err := jsonObject(src)
		if err != nil {
			return nil, nil, err
		}
		delete(m, "password")
		delete(m, "ics_url")
		redactURLFields(m)
		m["has_secret"] = hasSecret(src)
		values[src.ID] = m
		order = append(order, src.ID)
	}
	return values, order, nil
}

func destinationsView(list []DestinationConfig) (map[string]any, []string, error) {
	values := map[string]any{}
	order := []string{}
	for _, dst := range list {
		m, err := jsonObject(dst)
		if err != nil {
			return nil, nil, err
		}
		delete(m, "password")
		delete(m, "ics_url")
		redactURLFields(m)
		key := DestinationKey(dst.ID, dst.CalendarID)
		values[key] = m
		order = append(order, key)
	}
	return values, order, nil
}

// redactDecodeError rewrites a config decode error to name only the fields
// that failed. mapstructure messages quote the offending value ("..., value:
// '<v>'", "cannot parse 'f' as int: ... \"<v>\""), and that value can be a
// secret (an inline ics_url or password), while these errors end up in the
// startup output and the server log.
func redactDecodeError(err error) error {
	var me *mapstructure.Error
	items := []string{err.Error()}
	if errors.As(err, &me) {
		items = me.Errors
	}
	fields := make([]string, 0, len(items))
	for _, it := range items {
		if m := quotedField.FindStringSubmatch(it); m != nil {
			fields = append(fields, m[1])
		} else {
			fields = append(fields, "?")
		}
	}
	return fmt.Errorf("invalid value or type for %s (value not shown)", strings.Join(fields, ", "))
}

// quotedField matches the first single-quoted token of a mapstructure error
// item, which is the field path.
var quotedField = regexp.MustCompile(`'([^']*)'`)

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
	if id == "" || spec.Key(identity) != id {
		return validationf("%s: invalid id %q", coll, id)
	}
	// "|" separates id and calendar_id in destination keys, so it cannot
	// appear inside either (or in a source id a destination refers to).
	for _, f := range sortedKeys(identity) {
		if strings.Contains(str(identity[f]), "|") {
			return validationf("%s: %s must not contain \"|\"", path, f)
		}
	}
	if e == nil {
		return nil
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
