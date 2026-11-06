package config

import (
	"strings"
	"testing"
)

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{
		"Work Calendar":   "work-calendar",
		"a   b__c":        "a-b-c",
		"  --Hello!!  ":   "hello",
		"Team (EU) 2026!": "team-eu-2026",
		"日本語":             "calendar",
		"!!!":             "calendar",
		"":                "calendar",
	} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
	long := Slug(strings.Repeat("ab ", 40))
	if len(long) > 63 || strings.HasSuffix(long, "-") || !ValidID(long) {
		t.Errorf("long slug = %q (%d)", long, len(long))
	}
	if got := Slug(strings.Repeat("x", 100)); len(got) != 63 {
		t.Errorf("cut at 63: len = %d", len(got))
	}
}

func TestUniqueID(t *testing.T) {
	taken := map[string]bool{}
	isTaken := func(s string) bool { return taken[strings.ToLower(s)] }
	for _, want := range []string{"work", "work-2", "work-3"} {
		got := UniqueID("Work", isTaken)
		if got != want {
			t.Fatalf("UniqueID = %q, want %q", got, want)
		}
		taken[got] = true
	}
	// Existing ids in any case collide.
	taken = map[string]bool{"team": true}
	if got := UniqueID("TEAM", isTaken); got != "team-2" {
		t.Errorf("case-insensitive collision: %q", got)
	}
	// The reserved focacal id is skipped when the namespace reports it.
	reserved := func(s string) bool { return s == FocacalSource }
	if got := UniqueID("Focacal", reserved); got != "focacal-2" {
		t.Errorf("reserved id: %q", got)
	}
	// A suffix never pushes the id past 63 characters.
	base := strings.Repeat("x", 63)
	got := UniqueID(base, func(s string) bool { return s == base })
	if len(got) > 63 || got == base || !ValidID(got) {
		t.Errorf("suffix overflow: %q (%d)", got, len(got))
	}
}

func TestValidID(t *testing.T) {
	for _, ok := range []string{"work-google", "a", "0abc", "8d7c9a1e-3b4f-4c5d-8e6f-123456789abc", strings.Repeat("a", 63)} {
		if !ValidID(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "Work", "a:b", "a|b", "-x", "a b", "a_b", strings.Repeat("a", 64)} {
		if ValidID(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}
