package config

import (
	"reflect"
	"testing"

	"go.yaml.in/yaml/v3"
)

func parseMap(t *testing.T, src string) map[string]any {
	t.Helper()
	m := map[string]any{}
	if err := yaml.Unmarshal([]byte(src), &m); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m == nil {
		m = map[string]any{}
	}
	return m
}

// patchMap applies patch to the document src and returns the result.
func patchMap(t *testing.T, src map[string]any, patch map[string]any, c map[string]CollectionSpec) map[string]any {
	t.Helper()
	if err := ApplyMergePatch(src, patch, c); err != nil {
		t.Fatalf("ApplyMergePatch: %v", err)
	}
	return src
}

func expectMap(t *testing.T, got map[string]any, wantYAML string) {
	t.Helper()
	want := parseMap(t, wantYAML)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mismatch\n--- got ---\n%#v\n--- want ---\n%#v", got, want)
	}
}

var testCollections = map[string]CollectionSpec{"sources": SourcesSpec, "destinations": DestinationsSpec}

func TestPatchScalarReplaceKeepsSiblings(t *testing.T) {
	got := patchMap(t, parseMap(t, "working_hours:\n  start: \"09:00\"\n  end: \"17:00\"\n"),
		map[string]any{"working_hours": map[string]any{"end": "18:00"}}, nil)
	expectMap(t, got, "working_hours:\n  start: \"09:00\"\n  end: \"18:00\"\n")
}

func TestPatchKeepsUnknownKeys(t *testing.T) {
	src := "ollama:\n  host: localhost\n  extra: [1, 2]\nlookahead_days: 7\n"
	got := patchMap(t, parseMap(t, src), map[string]any{"lookahead_days": float64(14)}, nil)
	expectMap(t, got, "ollama:\n  host: localhost\n  extra: [1, 2]\nlookahead_days: 14\n")
}

func TestPatchNullDeletesScalar(t *testing.T) {
	got := patchMap(t, parseMap(t, "a: 1\nb: 2\nc: 3\n"), map[string]any{"b": nil, "missing": nil}, nil)
	expectMap(t, got, "a: 1\nc: 3\n")
}

func TestPatchCollectionAddEditDelete(t *testing.T) {
	src := "sources:\n  - id: a\n    label: Old\n  - id: b\n    label: Gone\n  - id: c\n    label: Keep\n"
	got := patchMap(t, parseMap(t, src), map[string]any{"sources": map[string]any{
		"a": map[string]any{"label": "X"},
		"b": nil,
	}}, testCollections)
	expectMap(t, got, "sources:\n  - id: a\n    label: X\n  - id: c\n    label: Keep\n")
}

func TestPatchNewCollectionItemAppended(t *testing.T) {
	src := "sources:\n  - id: a\n    label: A\n"
	got := patchMap(t, parseMap(t, src), map[string]any{"sources": map[string]any{
		"z": map[string]any{"label": "Z"},
		"m": map[string]any{"label": "M", "gone": nil},
		"q": nil,
	}}, testCollections)
	expectMap(t, got, "sources:\n  - id: a\n    label: A\n  - id: m\n    label: M\n  - id: z\n    label: Z\n")
}

func TestPatchEditedItemKeepsOtherFields(t *testing.T) {
	src := "sources:\n  - id: a\n    label: Old\n    color: red\n    nested:\n      x: 1\n      y: 2\n"
	got := patchMap(t, parseMap(t, src), map[string]any{"sources": map[string]any{
		"a": map[string]any{"label": "New", "nested": map[string]any{"y": float64(3), "x": nil}},
	}}, testCollections)
	expectMap(t, got, "sources:\n  - id: a\n    label: New\n    color: red\n    nested:\n      y: 3\n")
}

func TestPatchWholeNumberFloatWritesInteger(t *testing.T) {
	got := patchMap(t, parseMap(t, "a: 1\n"),
		map[string]any{"a": float64(60), "b": float64(1.5), "c": []any{float64(1), "x"}}, nil)
	expectMap(t, got, "a: 60\nb: 1.5\nc:\n  - 1\n  - x\n")
}

func TestPatchStringReplacesString(t *testing.T) {
	got := patchMap(t, parseMap(t, "a: \"x\"\nb: 'y'\nc: plain\n"), map[string]any{"a": "p", "b": "q", "c": "r"}, nil)
	expectMap(t, got, "a: p\nb: q\nc: r\n")
}

func TestPatchDestinationKeyCreatesIdentityFields(t *testing.T) {
	doc := patchMap(t, parseMap(t, "destinations: []\n"), map[string]any{"destinations": map[string]any{
		DestinationKey("g1", "cal@x"): map[string]any{"sources": []any{"a", "b"}},
	}}, testCollections)
	expectMap(t, doc, "destinations:\n  - id: g1\n    calendar_id: cal@x\n    sources:\n      - a\n      - b\n")

	// Existing item matched by composite key; nested list replaced wholesale.
	doc = patchMap(t, doc, map[string]any{"destinations": map[string]any{
		"g1|cal@x": map[string]any{"sources": []any{"c"}},
	}}, testCollections)
	expectMap(t, doc, "destinations:\n  - id: g1\n    calendar_id: cal@x\n    sources:\n      - c\n")

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

func TestPatchCollectionNotAListErrors(t *testing.T) {
	err := ApplyMergePatch(parseMap(t, "sources: nope\n"), map[string]any{"sources": map[string]any{
		"a": map[string]any{"label": "A"},
	}}, testCollections)
	if err == nil {
		t.Fatal("want error for a non-list collection")
	}
	err = ApplyMergePatch(map[string]any{}, map[string]any{"sources": "x"}, testCollections)
	if err == nil {
		t.Fatal("want error for a non-object collection patch")
	}
}

func TestPatchAbsentCollectionCreated(t *testing.T) {
	got := patchMap(t, parseMap(t, "a: 1\n"), map[string]any{"sources": map[string]any{
		"s1": map[string]any{"label": "One"},
	}}, testCollections)
	expectMap(t, got, "a: 1\nsources:\n  - id: s1\n    label: One\n")

	// Null deletes the whole collection; emptying leaves [].
	got = patchMap(t, got, map[string]any{"sources": map[string]any{"s1": nil}}, testCollections)
	expectMap(t, got, "a: 1\nsources: []\n")
	got = patchMap(t, got, map[string]any{"sources": nil}, testCollections)
	expectMap(t, got, "a: 1\n")
}

func TestPatchDeleteFirstAndOnlyKey(t *testing.T) {
	got := patchMap(t, parseMap(t, "a: 1\nb: 2\n"), map[string]any{"a": nil}, nil)
	expectMap(t, got, "b: 2\n")
	got = patchMap(t, parseMap(t, "a: 1\n"), map[string]any{"a": nil}, nil)
	if len(got) != 0 {
		t.Errorf("want empty doc, got %v", got)
	}
}
