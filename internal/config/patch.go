package config

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// CollectionSpec describes a top-level YAML sequence of mappings whose items
// are addressed by identity rather than position.
type CollectionSpec struct {
	// Key returns the identity of an existing item (decoded to a map).
	Key func(item map[string]any) string
	// Identity returns the identity fields for a new item created from key.
	Identity func(key string) map[string]any
}

// DestinationKey is the identity of a destination: source account id plus
// calendar id.
func DestinationKey(id, calendarID string) string { return id + "|" + calendarID }

func str(v any) string { s, _ := v.(string); return s }

// SourcesSpec identifies sources by their id.
var SourcesSpec = CollectionSpec{
	Key:      func(item map[string]any) string { return str(item["id"]) },
	Identity: func(key string) map[string]any { return map[string]any{"id": key} },
}

// DestinationsSpec identifies destinations by id and calendar_id.
var DestinationsSpec = CollectionSpec{
	Key: func(item map[string]any) string {
		return DestinationKey(str(item["id"]), str(item["calendar_id"]))
	},
	Identity: func(key string) map[string]any {
		id, cal, ok := strings.Cut(key, "|")
		if !ok {
			return map[string]any{"id": key}
		}
		return map[string]any{"id": id, "calendar_id": cal}
	},
}

// ApplyMergePatch applies an RFC 7396 merge patch to doc in place. Keys named
// in c are top-level collections: lists of maps in doc, objects keyed by item
// identity in patch, where a null entry removes the item, an unknown key
// appends a new item and a known key merges into the item where it stands.
// Values inserted from patch have nulls dropped and whole-number floats (as
// JSON decodes them) turned into ints.
func ApplyMergePatch(doc map[string]any, patch map[string]any, c map[string]CollectionSpec) error {
	return mergeMap(doc, patch, c)
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// mergeMap merges patch into m. c is non-nil only at the document root.
func mergeMap(m, patch map[string]any, c map[string]CollectionSpec) error {
	for _, key := range sortedKeys(patch) {
		val := patch[key]
		if val == nil {
			delete(m, key)
			continue
		}
		if spec, ok := c[key]; ok {
			entries, isMap := val.(map[string]any)
			if !isMap {
				return fmt.Errorf("collection %q: patch must be an object keyed by item id", key)
			}
			var list []any
			if cur, present := m[key]; present {
				var isList bool
				if list, isList = cur.([]any); !isList {
					return fmt.Errorf("collection %q: existing value is not a list", key)
				}
			} else {
				list = []any{}
			}
			list, err := mergeCollection(list, entries, spec)
			if err != nil {
				return fmt.Errorf("collection %q: %w", key, err)
			}
			m[key] = list
			continue
		}
		if sub, isMap := val.(map[string]any); isMap {
			if existing, ok := m[key].(map[string]any); ok {
				if err := mergeMap(existing, sub, nil); err != nil {
					return err
				}
				continue
			}
		}
		m[key] = clean(val)
	}
	return nil
}

func mergeCollection(list []any, entries map[string]any, spec CollectionSpec) ([]any, error) {
	for _, key := range sortedKeys(entries) {
		pos := -1
		for i, it := range list {
			if item, ok := it.(map[string]any); ok && spec.Key(item) == key {
				pos = i
				break
			}
		}
		switch e := entries[key].(type) {
		case nil:
			if pos >= 0 {
				list = append(list[:pos:pos], list[pos+1:]...)
			}
		case map[string]any:
			if pos >= 0 {
				if err := mergeMap(list[pos].(map[string]any), e, nil); err != nil {
					return nil, err
				}
				continue
			}
			item := spec.Identity(key)
			for k, v := range e {
				if v != nil {
					item[k] = clean(v)
				}
			}
			list = append(list, item)
		default:
			return nil, fmt.Errorf("item %q: patch must be an object or null", key)
		}
	}
	return list, nil
}

// clean returns v with nulls dropped from maps and whole-number floats
// converted to ints, without modifying v.
func clean(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			if e != nil {
				out[k] = clean(e)
			}
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = clean(e)
		}
		return out
	case float64:
		if t == math.Trunc(t) && math.Abs(t) < 1e15 {
			return int(t)
		}
	}
	return v
}
