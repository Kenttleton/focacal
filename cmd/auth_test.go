package cmd

import (
	"strings"
	"testing"

	"github.com/Kenttleton/focacal/internal/config"
)

func authCfg() *config.Config {
	c := &config.Config{}
	c.Accounts = []config.AccountConfig{
		{ID: "work", Type: "google"},
		{ID: "home", Type: "outlook"},
		{ID: "dav", Type: "caldav"},
		{ID: "empty", Type: "google"},
	}
	c.Calendars = []config.CalendarConfig{
		{ID: "work-team", Account: "work", RemoteID: "team@x"},
		{ID: "work-main", Account: "work"},
		{ID: "home-cal", Account: "home"},
		{ID: "dav-cal", Account: "dav"},
		{ID: "feed", URL: "https://x.example/a.ics"},
	}
	c.Resolve()
	return c
}

func TestAuthCmdResolvesAccountOrCalendarID(t *testing.T) {
	cfg := authCfg()
	for _, tc := range []struct {
		provider, id string
		wantID       string // resolved source id
		wantKey      string // token key
	}{
		{"google", "work", "work-team", "work"},      // account id: first calendar of the account
		{"google", "work-main", "work-main", "work"}, // calendar id of an OAuth account
		{"outlook", "home", "home-cal", "home"},
		{"google", "empty", "empty", "empty"}, // account without calendars
	} {
		got, err := findAuthSource(cfg, tc.provider, tc.id)
		if err != nil {
			t.Fatalf("%s %s: %v", tc.provider, tc.id, err)
		}
		if got.ID != tc.wantID || got.TokenKey() != tc.wantKey {
			t.Errorf("%s %s: source %q key %q, want %q key %q", tc.provider, tc.id, got.ID, got.TokenKey(), tc.wantID, tc.wantKey)
		}
	}
	for _, tc := range []struct{ provider, id, want string }{
		{"google", "nope", `"nope"`},
		{"google", "dav", "not an OAuth account"},
		{"google", "feed", "not an OAuth account"},
		{"google", "home", "not google"},
	} {
		_, err := findAuthSource(cfg, tc.provider, tc.id)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s %s: err = %v, want it to contain %q", tc.provider, tc.id, err, tc.want)
		}
	}
}
