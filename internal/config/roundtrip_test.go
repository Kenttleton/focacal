package config

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func boolPtr(b bool) *bool { return &b }

func fullSource() SourceConfig {
	return SourceConfig{
		ID:              "work-google",
		Label:           "Work",
		Type:            "google",
		CredentialsFile: "/creds.json",
		TokenFile:       "/token.json",
		CalendarIDs:     []string{"primary", "team"},
		Conflicts: ConflictConfig{
			Declined:  boolPtr(true),
			Tentative: boolPtr(false),
			Free:      boolPtr(true),
			AllDay:    boolPtr(true),
		},
		CalDAVURL: "https://caldav.example.com",
		Username:  "me",
		Password:  "pw",
		AuthType:  "basic",
		ICSURL:    "https://example.com/feed.ics",
		ICSURLEnv: "FEED_URL",
	}
}

func fullDestination() DestinationConfig {
	return DestinationConfig{
		ID:   "out",
		Name: "Out",
		Inputs: InputList{Items: []DestinationInput{
			{
				Calendar: "work-google",
				Event: EventVisibility{
					Title: FieldVisibility{Visibility: true, Rename: "Busy"},
					Body:  FieldVisibility{Visibility: false, Rename: ""},
				},
			},
			{Calendar: FocacalSource, Kinds: []string{"focus", "lunch"}},
		}},
		Target: DestinationTarget{Calendar: "team", Served: false},
	}
}

func fullAccount() AccountConfig {
	return AccountConfig{
		ID: "work-google", Name: "Work", Type: "caldav",
		CredentialsFile: "/c.json", TokenFile: "/t.json",
		CalDAVURL: "https://caldav.example.com", Username: "u", Password: "p", AuthType: "bearer",
	}
}

func fullCalendar() CalendarConfig {
	return CalendarConfig{
		ID: "work", Name: "Work", Account: "work-google", RemoteID: "primary", Plan: boolPtr(false),
		Conflicts: ConflictConfig{Declined: boolPtr(true)},
	}
}

func TestSourceConfigYAMLKeysAreSnakeCase(t *testing.T) {
	out, err := yaml.Marshal(fullSource())
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"credentials_file", "token_file", "calendar_ids", "caldav_url", "auth_type", "ics_url", "ics_url_env", "all_day"} {
		if !strings.Contains(s, want+":") {
			t.Errorf("yaml missing key %q:\n%s", want, s)
		}
	}
	for _, bad := range []string{"credentialsfile", "calendarids", "allday", "tokenfile", "caldavurl", "authtype", "icsurl"} {
		if strings.Contains(s, bad) {
			t.Errorf("yaml contains non-snake key %q:\n%s", bad, s)
		}
	}
}

func roundTrip[T any](t *testing.T, in T) {
	t.Helper()
	y, err := yaml.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	j, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	for kind, data := range map[string][]byte{"yaml": y, "json": j} {
		var got T
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		var err error
		if kind == "json" {
			err = json.Unmarshal(data, &got)
		} else {
			err = dec.Decode(&got)
		}
		if err != nil {
			t.Fatalf("%s unmarshal: %v", kind, err)
		}
		if !reflect.DeepEqual(in, got) {
			t.Errorf("%s round trip mismatch:\n in: %+v\ngot: %+v\ndata: %s", kind, in, got, data)
		}
	}
}

func TestSourceConfigRoundTrip(t *testing.T) {
	roundTrip(t, fullSource())
}

func TestSourceConfigRoundTripNilConflicts(t *testing.T) {
	in := fullSource()
	in.Conflicts = ConflictConfig{}
	roundTrip(t, in)
}

func TestDestinationConfigRoundTrip(t *testing.T) {
	roundTrip(t, fullDestination())
}

func TestAccountAndCalendarRoundTrip(t *testing.T) {
	roundTrip(t, fullAccount())
	roundTrip(t, fullCalendar())
	roundTrip(t, CalendarConfig{ID: "feed", URL: "https://x/y.ics", URLEnv: "FOCACAL_FEED"})
}

func TestDestinationAllInputsRoundTrip(t *testing.T) {
	in := DestinationConfig{ID: "out", Inputs: InputList{All: true}, Target: DestinationTarget{Served: true, Auth: "private"}}
	roundTrip(t, in)
	y, err := yaml.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(y), "inputs: all") {
		t.Errorf("all should marshal as a scalar:\n%s", y)
	}
	j, _ := json.Marshal(in)
	if !strings.Contains(string(j), `"inputs":"all"`) {
		t.Errorf("all should marshal as a JSON string: %s", j)
	}
}

func TestInputListRejectsOtherScalars(t *testing.T) {
	var l InputList
	if err := yaml.Unmarshal([]byte("some"), &l); err == nil {
		t.Error("yaml scalar other than all must fail")
	}
	if err := json.Unmarshal([]byte(`"some"`), &l); err == nil {
		t.Error("json string other than all must fail")
	}
	if err := json.Unmarshal([]byte(`5`), &l); err == nil {
		t.Error("json number must fail")
	}
}

func TestUserSectionsRoundTrip(t *testing.T) {
	roundTrip(t, WorkingHours{Start: "09:00", End: "18:00", Timezone: "America/New_York"})
	roundTrip(t, LunchConfig{Preferred: "12:00", WindowStart: "11:30", WindowEnd: "13:30", DurationMinutes: 60})
	roundTrip(t, FocusConfig{Style: "pomodoro", MinBlockMinutes: 30, MaxBlockMinutes: 120, BufferAroundMeetings: 10, PomodoroWorkMinutes: 25, PomodoroBreakMinutes: 5})
}

func TestConflictConfigNilStaysNil(t *testing.T) {
	j, err := json.Marshal(ConflictConfig{})
	if err != nil {
		t.Fatal(err)
	}
	var c ConflictConfig
	if err := json.Unmarshal(j, &c); err != nil {
		t.Fatal(err)
	}
	if c.Declined != nil || c.Tentative != nil || c.Free != nil || c.AllDay != nil {
		t.Errorf("nil pointers became non-nil: %s", j)
	}
}
