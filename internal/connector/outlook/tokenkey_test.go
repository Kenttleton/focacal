package outlook

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Kenttleton/focacal/internal/config"
	"github.com/Kenttleton/focacal/internal/store"
	"golang.org/x/oauth2"
)

func TestOutlookUsesTokenKey(t *testing.T) {
	t.Setenv("OUTLOOK_CLIENT_ID", "id")
	t.Setenv("OUTLOOK_CLIENT_SECRET", "secret")
	ctx := context.Background()
	for _, tc := range []struct {
		name, account, id, key string
	}{
		{"account token shared", "work", "work-team", "work"},
		{"no account uses calendar id", "", "solo", "solo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err := db.SetOAuthToken(ctx, tc.key, `{"access_token":"a","refresh_token":"r"}`); err != nil {
				t.Fatal(err)
			}
			c, err := New(config.SourceConfig{ID: tc.id, AccountID: tc.account, Type: "outlook"}, db, "http://localhost", "common")
			if err != nil {
				t.Fatal(err)
			}
			if !c.IsAuthorized() {
				t.Fatalf("token stored under %q was not found for id %q", tc.key, tc.id)
			}
			if got := c.SourceID(); got != tc.id {
				t.Fatalf("SourceID = %q, want the calendar id %q", got, tc.id)
			}
			// persist writes under the token key, not the calendar id.
			if err := db.SetOAuthToken(ctx, tc.key, ""); err != nil {
				t.Fatal(err)
			}
			if err := c.persistToken(ctx, sampleToken()); err != nil {
				t.Fatal(err)
			}
			if got, _ := db.GetOAuthToken(ctx, tc.key); got == "" {
				t.Fatalf("persistToken did not write under %q", tc.key)
			}
			if tc.key != tc.id {
				if got, _ := db.GetOAuthToken(ctx, tc.id); got != "" {
					t.Fatalf("persistToken wrote under calendar id %q", tc.id)
				}
			}
		})
	}
}

// Client credentials follow the account too: OUTLOOK_CLIENT_ID_<ACCOUNT>.
func TestOutlookClientCredentialsFollowAccount(t *testing.T) {
	t.Setenv("OUTLOOK_CLIENT_ID_WORK", "acct-id")
	t.Setenv("OUTLOOK_CLIENT_SECRET_WORK", "acct-secret")
	id, secret, err := loadClientCredentials(config.SourceConfig{ID: "work-team", AccountID: "work"})
	if err != nil || id != "acct-id" || secret != "acct-secret" {
		t.Fatalf("got %q %q %v", id, secret, err)
	}
	t.Setenv("OUTLOOK_CLIENT_ID_SOLO_CAL", "own")
	id, _, err = loadClientCredentials(config.SourceConfig{ID: "solo-cal"})
	if err != nil || id != "own" {
		t.Fatalf("got %q %v", id, err)
	}
}

func sampleToken() *oauth2.Token { return &oauth2.Token{AccessToken: "a", RefreshToken: "r"} }
