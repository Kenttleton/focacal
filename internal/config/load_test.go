package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestDecodeDomain_StrictRejectsUnknownKey(t *testing.T) {
	c := Defaults()
	err := decodeDomain(DomainPlanner, []byte("working_hours:\n  strt: \"SECRETVAL\"\n"), c)
	if err == nil {
		t.Fatal("expected error for unknown key")
	}
	if !strings.Contains(err.Error(), "strt") {
		t.Errorf("error should name the key: %v", err)
	}
	if strings.Contains(err.Error(), "SECRETVAL") {
		t.Errorf("error echoes the value: %v", err)
	}
}

func TestDecodeDomain_OnlyOwnKeys(t *testing.T) {
	c := Defaults()
	in := "working_hours:\n  start: \"08:00\"\nsources:\n  - id: stray\n    type: ics\n"
	if err := decodeDomain(DomainApp, []byte(in), c); err != nil {
		t.Fatalf("stray keys of other domains must be ignored: %v", err)
	}
	if len(c.Sources) != 0 || c.WorkingHours.Start != "09:00" {
		t.Errorf("other domain's keys leaked: %+v", c)
	}
	if err := decodeDomain(DomainApp, []byte("server:\n  rest_prt: 1\n"), c); err == nil {
		t.Error("typo inside an owned key must be an error")
	}
}

func TestDecodeDomain_EmptyIsNoop(t *testing.T) {
	c := Defaults()
	for _, in := range []string{"", "  \n", "# only a comment\n"} {
		if err := decodeDomain(DomainPlanner, []byte(in), c); err != nil {
			t.Errorf("%q: %v", in, err)
		}
	}
	if !reflect.DeepEqual(c, Defaults()) {
		t.Error("empty input changed the config")
	}
}

func TestDecodeDomain_TypeErrorNamesFieldHidesValue(t *testing.T) {
	c := Defaults()
	err := decodeDomain(DomainApp, []byte("server:\n  rest_port: LEAKME\n"), c)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "LEAKME") || !strings.Contains(err.Error(), "rest_port") {
		t.Errorf("bad error: %v", err)
	}
}

func TestDefaultsApplyWhenKeyAbsent(t *testing.T) {
	dir := t.TempDir()
	got, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, Defaults()) {
		t.Errorf("empty dir != Defaults():\n%+v\n%+v", got, Defaults())
	}
	// A partial key keeps the other defaults.
	c := Defaults()
	if err := decodeDomain(DomainPlanner, []byte("working_hours:\n  start: \"07:00\"\n"), c); err != nil {
		t.Fatal(err)
	}
	if c.WorkingHours.Start != "07:00" || c.WorkingHours.End != "18:00" || c.WorkingHours.Timezone != "UTC" {
		t.Errorf("partial key lost defaults: %+v", c.WorkingHours)
	}
}

func TestColorsKeysKeepCase(t *testing.T) {
	c := Defaults()
	if err := decodeDomain(DomainPlanner, []byte("colors:\n  Meeting: \"#fff\"\n"), c); err != nil {
		t.Fatal(err)
	}
	if c.Colors["Meeting"] != "#fff" {
		t.Errorf("colors = %v", c.Colors)
	}
}

// TestDecodeDomain_LegacyAuthCredentialKeys: an unmigrated config.yaml still
// holds auth.username etc.; they must not trip strict decoding.
func TestDecodeDomain_LegacyAuthCredentialKeys(t *testing.T) {
	c := Defaults()
	in := "auth:\n  enabled: false\n  username: u\n  password_hash: h\n  session_token: s\n"
	if err := decodeDomain(DomainApp, []byte(in), c); err != nil {
		t.Fatal(err)
	}
	if c.Auth.Enabled {
		t.Error("auth.enabled not decoded")
	}
}

func TestDecodeDomain_ObsoleteSourceKeysDroppedTyposRejected(t *testing.T) {
	c := Defaults()
	ok := "sources:\n  - id: a\n    type: google\n    writeback: true\n    cross_busy: false\n"
	if err := decodeDomain(DomainCalendars, []byte(ok), c); err != nil {
		t.Fatal(err)
	}
	if len(c.Sources) != 1 || c.Sources[0].ID != "a" {
		t.Errorf("sources = %+v", c.Sources)
	}
	typo := "sources:\n  - id: a\n    type: google\n    calendar_idz: [x]\n"
	err := decodeDomain(DomainCalendars, []byte(typo), Defaults())
	if err == nil || !strings.Contains(err.Error(), "calendar_idz") {
		t.Errorf("typo not rejected by name: %v", err)
	}
}
