package config

import (
	"bytes"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func parseDoc(t *testing.T, src string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	return &doc
}

func encodeDoc(t *testing.T, doc *yaml.Node) string {
	t.Helper()
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		t.Fatalf("encode: %v", err)
	}
	_ = enc.Close()
	return buf.String()
}

func patchYAML(t *testing.T, src string, patch map[string]any, c map[string]CollectionSpec) string {
	t.Helper()
	doc := parseDoc(t, src)
	if err := ApplyMergePatch(doc, patch, c); err != nil {
		t.Fatalf("ApplyMergePatch: %v", err)
	}
	return encodeDoc(t, doc)
}

func expectYAML(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("yaml mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

var testCollections = map[string]CollectionSpec{"sources": SourcesSpec, "destinations": DestinationsSpec}

func TestPatchKeepsCommentsOnUntouchedKeys(t *testing.T) {
	src := "working_hours:\n  start: \"09:00\" # morning\n  end: \"17:00\"\n"
	got := patchYAML(t, src, map[string]any{"working_hours": map[string]any{"end": "18:00"}}, nil)
	expectYAML(t, got, "working_hours:\n  start: \"09:00\" # morning\n  end: \"18:00\"\n")
}

func TestPatchKeepsUnknownKeys(t *testing.T) {
	src := "# top\nollama:\n  # custom\n  host: localhost   # keep\n  extra: [1, 2]\nlookahead_days: 7\n"
	got := patchYAML(t, src, map[string]any{"lookahead_days": float64(14)}, nil)
	expectYAML(t, got, "# top\nollama:\n  # custom\n  host: localhost # keep\n  extra: [1, 2]\nlookahead_days: 14\n")
}

func TestPatchNullDeletesScalar(t *testing.T) {
	src := "a: 1\nb: 2\nc: 3\n"
	got := patchYAML(t, src, map[string]any{"b": nil, "missing": nil}, nil)
	expectYAML(t, got, "a: 1\nc: 3\n")
}

func TestPatchCollectionAddEditDelete(t *testing.T) {
	src := "sources:\n  - id: a\n    label: Old\n  - id: b\n    label: Gone\n  - id: c\n    label: Keep\n"
	got := patchYAML(t, src, map[string]any{"sources": map[string]any{
		"a": map[string]any{"label": "X"},
		"b": nil,
	}}, testCollections)
	expectYAML(t, got, "sources:\n  - id: a\n    label: X\n  - id: c\n    label: Keep\n")
}

func TestPatchNewCollectionItemAppended(t *testing.T) {
	src := "sources:\n  - id: a\n    label: A\n"
	got := patchYAML(t, src, map[string]any{"sources": map[string]any{
		"z": map[string]any{"label": "Z"},
		"m": map[string]any{"label": "M", "gone": nil},
		"q": nil,
	}}, testCollections)
	expectYAML(t, got, "sources:\n  - id: a\n    label: A\n  - id: m\n    label: M\n  - id: z\n    label: Z\n")
}

func TestPatchPreservesNestedCommentsInEditedItem(t *testing.T) {
	src := "sources:\n  - id: a\n    # the label\n    label: Old # inline\n    color: red   # keep me\n"
	got := patchYAML(t, src, map[string]any{"sources": map[string]any{
		"a": map[string]any{"label": "New"},
	}}, testCollections)
	expectYAML(t, got, "sources:\n  - id: a\n    # the label\n    label: New # inline\n    color: red # keep me\n")
}

func TestPatchWholeNumberFloatWritesInteger(t *testing.T) {
	got := patchYAML(t, "a: 1\n", map[string]any{"a": float64(60), "b": float64(1.5), "c": []any{float64(1), "x"}}, nil)
	expectYAML(t, got, "a: 60\nb: 1.5\nc:\n  - 1\n  - x\n")
}

func TestPatchQuotedStringKeepsQuotes(t *testing.T) {
	src := "a: \"x\"\nb: 'y'\nc: plain\n"
	got := patchYAML(t, src, map[string]any{"a": "p", "b": "q", "c": "r"}, nil)
	expectYAML(t, got, "a: \"p\"\nb: 'q'\nc: r\n")
}

func TestPatchDestinationKeyCreatesIdentityFields(t *testing.T) {
	src := "destinations: []\n"
	got := patchYAML(t, src, map[string]any{"destinations": map[string]any{
		DestinationKey("g1", "cal@x"): map[string]any{"sources": []any{"a", "b"}},
	}}, testCollections)
	expectYAML(t, got, "destinations:\n  - id: g1\n    calendar_id: cal@x\n    sources:\n      - a\n      - b\n")

	// Existing item matched by composite key; nested list replaced wholesale.
	got = patchYAML(t, got, map[string]any{"destinations": map[string]any{
		"g1|cal@x": map[string]any{"sources": []any{"c"}},
	}}, testCollections)
	expectYAML(t, got, "destinations:\n  - id: g1\n    calendar_id: cal@x\n    sources:\n      - c\n")

	if DestinationKey("a", "b") != "a|b" {
		t.Error("DestinationKey format")
	}
	if id := DestinationsSpec.Identity("only"); len(id) != 1 || id["id"] != "only" {
		t.Errorf("identity without pipe: %v", id)
	}
	if id := DestinationsSpec.Identity("a|b|c"); id["id"] != "a" || id["calendar_id"] != "b|c" {
		t.Errorf("identity splits on first pipe: %v", id)
	}
}

func TestPatchRootNotMappingErrors(t *testing.T) {
	doc := parseDoc(t, "- a\n- b\n")
	err := ApplyMergePatch(doc, map[string]any{"a": "b"}, nil)
	if err == nil || !strings.Contains(err.Error(), "mapping") {
		t.Fatalf("want mapping error, got %v", err)
	}
}

func TestPatchAbsentCollectionCreated(t *testing.T) {
	got := patchYAML(t, "a: 1\n", map[string]any{"sources": map[string]any{
		"s1": map[string]any{"label": "One"},
	}}, testCollections)
	expectYAML(t, got, "a: 1\nsources:\n  - id: s1\n    label: One\n")

	// Null deletes the whole collection; emptying leaves [].
	got = patchYAML(t, got, map[string]any{"sources": map[string]any{"s1": nil}}, testCollections)
	expectYAML(t, got, "a: 1\nsources: []\n")
	got = patchYAML(t, got, map[string]any{"sources": nil}, testCollections)
	expectYAML(t, got, "a: 1\n")
}
