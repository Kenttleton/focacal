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

var testCollections = map[string]CollectionSpec{"calendars": IDSpec, "destinations": IDSpec}

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
	src := "calendars:\n  - id: a\n    name: Old\n  - id: b\n    name: Gone\n  - id: c\n    name: Keep\n"
	got := patchMap(t, parseMap(t, src), map[string]any{"calendars": map[string]any{
		"a": map[string]any{"name": "X"},
		"b": nil,
	}}, testCollections)
	expectMap(t, got, "calendars:\n  - id: a\n    name: X\n  - id: c\n    name: Keep\n")
}

func TestPatchNewCollectionItemAppended(t *testing.T) {
	src := "calendars:\n  - id: a\n    name: A\n"
	got := patchMap(t, parseMap(t, src), map[string]any{"calendars": map[string]any{
		"z": map[string]any{"name": "Z"},
		"m": map[string]any{"name": "M", "gone": nil},
		"q": nil,
	}}, testCollections)
	expectMap(t, got, "calendars:\n  - id: a\n    name: A\n  - id: m\n    name: M\n  - id: z\n    name: Z\n")
}

func TestPatchEditedItemKeepsOtherFields(t *testing.T) {
	src := "calendars:\n  - id: a\n    name: Old\n    color: red\n    nested:\n      x: 1\n      y: 2\n"
	got := patchMap(t, parseMap(t, src), map[string]any{"calendars": map[string]any{
		"a": map[string]any{"name": "New", "nested": map[string]any{"y": float64(3), "x": nil}},
	}}, testCollections)
	expectMap(t, got, "calendars:\n  - id: a\n    name: New\n    color: red\n    nested:\n      y: 3\n")
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

func TestPatchDestinationCreatesIdentityField(t *testing.T) {
	doc := patchMap(t, parseMap(t, "destinations: []\n"), map[string]any{"destinations": map[string]any{
		"out": map[string]any{"inputs": "all"},
	}}, testCollections)
	expectMap(t, doc, "destinations:\n  - id: out\n    inputs: all\n")

	// Existing item matched by id; nested value replaced wholesale.
	doc = patchMap(t, doc, map[string]any{"destinations": map[string]any{
		"out": map[string]any{"inputs": []any{map[string]any{"calendar": "c"}}},
	}}, testCollections)
	expectMap(t, doc, "destinations:\n  - id: out\n    inputs:\n      - calendar: c\n")

	if id := IDSpec.Identity("only"); len(id) != 1 || id["id"] != "only" {
		t.Errorf("identity: %v", id)
	}
}

func TestPatchCollectionNotAListErrors(t *testing.T) {
	err := ApplyMergePatch(parseMap(t, "calendars: nope\n"), map[string]any{"calendars": map[string]any{
		"a": map[string]any{"name": "A"},
	}}, testCollections)
	if err == nil {
		t.Fatal("want error for a non-list collection")
	}
	err = ApplyMergePatch(map[string]any{}, map[string]any{"calendars": "x"}, testCollections)
	if err == nil {
		t.Fatal("want error for a non-object collection patch")
	}
}

func TestPatchAbsentCollectionCreated(t *testing.T) {
	got := patchMap(t, parseMap(t, "a: 1\n"), map[string]any{"calendars": map[string]any{
		"s1": map[string]any{"name": "One"},
	}}, testCollections)
	expectMap(t, got, "a: 1\ncalendars:\n  - id: s1\n    name: One\n")

	// Null deletes the whole collection; emptying leaves [].
	got = patchMap(t, got, map[string]any{"calendars": map[string]any{"s1": nil}}, testCollections)
	expectMap(t, got, "a: 1\ncalendars: []\n")
	got = patchMap(t, got, map[string]any{"calendars": nil}, testCollections)
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
