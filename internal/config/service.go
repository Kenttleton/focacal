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
	"strings"

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
// empty. started is the config the process is running with; it is used for
// pending_restart and is never modified. A nil started disables the flag.
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
func loadFiles(files map[Domain][]byte) (*Config, error) {
	v := viper.New()
	applyDefaults(v)
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
		return nil, fmt.Errorf("unmarshal config: %w", err)
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
			return fmt.Errorf("%w: %w", ErrValidation, err)
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
		for k, v := range m {
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
func projection(c *Config, d Domain) any {
	var out []any
	for _, dk := range domainKeys[d] {
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
	return s.Password != "" || s.ICSURL != "" || s.ICSURLEnv != "" || s.CredentialsFile != ""
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
		key := DestinationKey(dst.ID, dst.CalendarID)
		values[key] = m
		order = append(order, key)
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
	if id == "" || spec.Key(identity) != id {
		return validationf("%s: invalid id %q", coll, id)
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
