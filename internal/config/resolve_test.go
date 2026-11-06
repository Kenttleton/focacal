package config

import (
	"reflect"
	"testing"
)

func TestResolveAccountCalendars(t *testing.T) {
	c := &Config{}
	c.Accounts = []AccountConfig{{
		ID: "work-google", Name: "Work", Type: "caldav",
		CredentialsFile: "/c.json", TokenFile: "/t.json",
		CalDAVURL: "https://dav.example", Username: "me", Password: "pw", AuthType: "bearer",
	}}
	c.Calendars = []CalendarConfig{
		{ID: "main", Name: "Main", Account: "work-google", RemoteID: "primary", Conflicts: ConflictConfig{Free: boolPtr(true)}},
		{ID: "team", Name: "Team", Account: "work-google", RemoteID: "team@x", Plan: boolPtr(false)},
	}
	c.Resolve()
	if len(c.Sources) != 2 {
		t.Fatalf("sources = %d, want 2", len(c.Sources))
	}
	got := c.Sources[0]
	want := SourceConfig{
		ID: "main", Label: "Main", Type: "caldav",
		CredentialsFile: "/c.json", TokenFile: "/t.json",
		CalDAVURL: "https://dav.example", Username: "me", Password: "pw", AuthType: "bearer",
		CalendarIDs: []string{"primary"}, AccountID: "work-google", Plan: true,
		Conflicts: ConflictConfig{Free: boolPtr(true)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("source[0]:\n got %+v\nwant %+v", got, want)
	}
	if s := c.Sources[1]; s.ID != "team" || s.Plan || !reflect.DeepEqual(s.CalendarIDs, []string{"team@x"}) {
		t.Errorf("source[1] = %+v", s)
	}
	if got.TokenKey() != "work-google" {
		t.Errorf("TokenKey = %q, want the account id", got.TokenKey())
	}
	if ids := c.PlanSourceIDs(); !reflect.DeepEqual(ids, map[string]bool{"main": true}) {
		t.Errorf("PlanSourceIDs = %v", ids)
	}
}

func TestResolveAccountWithoutRemoteIDUsesConnectorDefault(t *testing.T) {
	c := &Config{}
	c.Accounts = []AccountConfig{{ID: "g", Type: "google"}}
	c.Calendars = []CalendarConfig{{ID: "main", Account: "g"}}
	c.Resolve()
	if len(c.Sources) != 1 || c.Sources[0].CalendarIDs != nil {
		t.Fatalf("sources = %+v", c.Sources)
	}
	if got := c.Sources[0].EffectiveCalendarIDs(); !reflect.DeepEqual(got, []string{"primary"}) {
		t.Errorf("EffectiveCalendarIDs = %v", got)
	}
}

func TestResolveURLCalendar(t *testing.T) {
	c := &Config{}
	c.Calendars = []CalendarConfig{
		{ID: "feed", Name: "Feed", URL: "https://x/y.ics"},
		{ID: "env", URLEnv: "FOCACAL_ENV_FEED"},
	}
	c.Resolve()
	if len(c.Sources) != 2 {
		t.Fatalf("sources = %+v", c.Sources)
	}
	s := c.Sources[0]
	if s.Type != "ics" || s.ICSURL != "https://x/y.ics" || s.AccountID != "" || s.Label != "Feed" || !s.Plan {
		t.Errorf("feed = %+v", s)
	}
	if e := c.Sources[1]; e.Type != "ics" || e.ICSURLEnv != "FOCACAL_ENV_FEED" || e.TokenKey() != "env" {
		t.Errorf("env feed = %+v", e)
	}
}

func TestResolveDanglingAccountSkipped(t *testing.T) {
	c := &Config{}
	c.Accounts = []AccountConfig{{ID: "g", Type: "google"}}
	c.Calendars = []CalendarConfig{
		{ID: "ghost", Account: "missing"},
		{ID: "ok", Account: "g"},
	}
	c.Resolve()
	if len(c.Sources) != 1 || c.Sources[0].ID != "ok" {
		t.Fatalf("sources = %+v, want only ok", c.Sources)
	}
	// Resolve replaces a previous derivation rather than appending.
	c.Resolve()
	if len(c.Sources) != 1 {
		t.Errorf("second Resolve: %d sources", len(c.Sources))
	}
}

func TestExpandInputsAllExcludesTarget(t *testing.T) {
	c := &Config{}
	c.Accounts = []AccountConfig{{ID: "g", Type: "google"}}
	c.Calendars = []CalendarConfig{
		{ID: "work", Account: "g"},
		{ID: "home", Account: "g", RemoteID: "home"},
		{ID: "feed", URL: "https://x/y.ics"},
		{ID: "ghost", Account: "missing"},
	}
	d := DestinationConfig{ID: "out", Inputs: InputList{All: true}, Target: DestinationTarget{Calendar: "work"}}
	got := c.ExpandInputs(d)
	want := []DestinationInput{{Calendar: "home"}, {Calendar: "feed"}, {Calendar: FocacalSource}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("all with target work:\n got %+v\nwant %+v", got, want)
	}

	served := DestinationConfig{ID: "pub", Inputs: InputList{All: true}, Target: DestinationTarget{Served: true}}
	if got := c.ExpandInputs(served); len(got) != 4 || got[3].Calendar != FocacalSource {
		t.Errorf("all with served target = %+v", got)
	}
}

func TestExpandInputsListDropsUnknown(t *testing.T) {
	c := &Config{}
	c.Calendars = []CalendarConfig{{ID: "feed", URL: "https://x/y.ics"}}
	vis := EventVisibility{Title: FieldVisibility{Visibility: true, Rename: "Busy"}}
	d := DestinationConfig{ID: "out", Inputs: InputList{Items: []DestinationInput{
		{Calendar: "feed", Event: vis},
		{Calendar: "gone"},
		{Calendar: FocacalSource, Kinds: []string{"focus"}},
	}}}
	got := c.ExpandInputs(d)
	if len(got) != 2 || got[0].Calendar != "feed" || got[0].Event != vis || got[1].Calendar != FocacalSource {
		t.Errorf("ExpandInputs = %+v", got)
	}
}

func TestLoadFilesResolves(t *testing.T) {
	cal := "accounts:\n  - id: g\n    type: google\ncalendars:\n  - id: work\n    account: g\n"
	c, err := loadFiles(map[Domain][]byte{DomainCalendars: []byte(cal)})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Sources) != 1 || c.Sources[0].ID != "work" || c.Sources[0].Type != "google" {
		t.Errorf("sources = %+v", c.Sources)
	}
}
