package config

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
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

// ApplyMergePatch applies an RFC 7396 merge patch to the YAML document doc,
// preserving comments, key order and unknown keys of untouched content. Keys
// named in c are top-level collections whose patch value is an object keyed
// by item identity; a null entry removes the item.
func ApplyMergePatch(doc *yaml.Node, patch map[string]any, c map[string]CollectionSpec) error {
	root := doc
	if root.Kind == yaml.DocumentNode {
		if len(root.Content) == 0 {
			return errors.New("config document is empty")
		}
		root = root.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		return errors.New("config root is not a mapping")
	}
	return mergeMapping(root, patch, c)
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// findKey returns the index of key's value node in mapping m, or -1.
func findKey(m *yaml.Node, key string) int {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return i + 1
		}
	}
	return -1
}

func removePair(m *yaml.Node, valIdx int) {
	m.Content = append(m.Content[:valIdx-1], m.Content[valIdx+1:]...)
}

// mergeMapping merges patch into m. c is non-nil only at the document root.
func mergeMapping(m *yaml.Node, patch map[string]any, c map[string]CollectionSpec) error {
	for _, key := range sortedKeys(patch) {
		val := patch[key]
		idx := findKey(m, key)

		if val == nil {
			if idx >= 0 {
				removePair(m, idx)
			}
			continue
		}

		if spec, ok := c[key]; ok {
			entries, isMap := val.(map[string]any)
			if !isMap {
				return fmt.Errorf("collection %q: patch must be an object keyed by item id", key)
			}
			if idx < 0 {
				m.Content = append(m.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
					&yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"})
				idx = len(m.Content) - 1
			}
			if m.Content[idx].Kind != yaml.SequenceNode {
				return fmt.Errorf("collection %q: existing value is not a list", key)
			}
			if err := mergeCollection(m.Content[idx], entries, spec); err != nil {
				return fmt.Errorf("collection %q: %w", key, err)
			}
			continue
		}

		if idx < 0 {
			m.Content = append(m.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
				valueNode(val))
			continue
		}
		existing := m.Content[idx]
		if sub, isMap := val.(map[string]any); isMap && existing.Kind == yaml.MappingNode {
			if err := mergeMapping(existing, sub, nil); err != nil {
				return err
			}
			continue
		}
		m.Content[idx] = replaceValue(existing, val)
	}
	return nil
}

func mergeCollection(seq *yaml.Node, entries map[string]any, spec CollectionSpec) error {
	// An empty "[]" is flow style; once items are added use block style.
	if len(seq.Content) == 0 {
		defer func() {
			if len(seq.Content) > 0 {
				seq.Style &^= yaml.FlowStyle
			}
		}()
	}
	for _, key := range sortedKeys(entries) {
		entry := entries[key]
		pos := -1
		for i, item := range seq.Content {
			if item.Kind != yaml.MappingNode {
				continue
			}
			var decoded map[string]any
			if err := item.Decode(&decoded); err != nil {
				return fmt.Errorf("decode item: %w", err)
			}
			if spec.Key(decoded) == key {
				pos = i
				break
			}
		}
		switch e := entry.(type) {
		case nil:
			if pos >= 0 {
				seq.Content = append(seq.Content[:pos], seq.Content[pos+1:]...)
			}
		case map[string]any:
			if pos >= 0 {
				if err := mergeMapping(seq.Content[pos], e, nil); err != nil {
					return err
				}
				continue
			}
			seq.Content = append(seq.Content, newItem(spec.Identity(key), e))
		default:
			return fmt.Errorf("item %q: patch must be an object or null", key)
		}
	}
	return nil
}

// newItem builds a mapping with identity fields first (id leading), then the
// entry's own fields, skipping identity fields the entry overrides.
func newItem(identity, entry map[string]any) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	keys := sortedKeys(identity)
	sort.SliceStable(keys, func(i, j int) bool { return keys[i] == "id" && keys[j] != "id" })
	for _, k := range keys {
		if _, overridden := entry[k]; overridden {
			continue
		}
		n.Content = append(n.Content, keyNode(k), valueNode(identity[k]))
	}
	appendFields(n, entry)
	return n
}

func keyNode(k string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k}
}

func appendFields(n *yaml.Node, fields map[string]any) {
	for _, k := range sortedKeys(fields) {
		if fields[k] == nil {
			continue
		}
		n.Content = append(n.Content, keyNode(k), valueNode(fields[k]))
	}
}

// valueNode builds a fresh node for a JSON-decoded value, dropping nulls
// inside maps.
func valueNode(v any) *yaml.Node {
	switch t := v.(type) {
	case map[string]any:
		n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		appendFields(n, t)
		return n
	case []any:
		n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, e := range t {
			n.Content = append(n.Content, valueNode(e))
		}
		return n
	case float64:
		if t == math.Trunc(t) && math.Abs(t) < 1e15 {
			return valueNode(int64(t))
		}
	}
	n := &yaml.Node{}
	if err := n.Encode(v); err != nil {
		// Unencodable values cannot occur for JSON-decoded input; fall back
		// to their textual form rather than panic.
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: fmt.Sprint(v)}
	}
	return n
}

// replaceValue returns the node that should replace old for new value v. A
// scalar replacing a scalar keeps the old comments and quoting style.
func replaceValue(old *yaml.Node, v any) *yaml.Node {
	n := valueNode(v)
	if old.Kind == yaml.ScalarNode && n.Kind == yaml.ScalarNode {
		n.HeadComment, n.LineComment, n.FootComment = old.HeadComment, old.LineComment, old.FootComment
		if _, isStr := v.(string); isStr && (old.Style&yaml.DoubleQuotedStyle != 0 || old.Style&yaml.SingleQuotedStyle != 0) {
			n.Style = old.Style & (yaml.DoubleQuotedStyle | yaml.SingleQuotedStyle)
		}
	}
	return n
}
