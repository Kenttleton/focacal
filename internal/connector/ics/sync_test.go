package ics

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Kenttleton/focacal/internal/config"
)

// An unresolved ics_url_env fails this source's sync with an error naming
// the source and the variable (R14), without touching the DB.
func TestSyncUnresolvedEnvRef(t *testing.T) {
	os.Unsetenv("FOCACAL_TEST_UNSET_FEED")
	c, _ := New(config.SourceConfig{ID: "team", Type: "ics", ICSURLEnv: "FOCACAL_TEST_UNSET_FEED"}, nil)
	err := c.Sync(context.Background())
	if err == nil {
		t.Fatal("Sync succeeded without a feed URL")
	}
	for _, want := range []string{"ics[team]", "FOCACAL_TEST_UNSET_FEED"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err %q should contain %q", err, want)
		}
	}
}

// A feed URL that does not parse must not be echoed: it is a secret.
func TestSyncBadURLNotEchoed(t *testing.T) {
	t.Setenv("FOCACAL_TEST_BAD_FEED", "https://x/feed.ics?token=SECRET\x7f")
	c, _ := New(config.SourceConfig{ID: "team", Type: "ics", ICSURLEnv: "FOCACAL_TEST_BAD_FEED"}, nil)
	err := c.Sync(context.Background())
	if err == nil {
		t.Fatal("Sync succeeded with an invalid URL")
	}
	if strings.Contains(err.Error(), "SECRET") || !strings.Contains(err.Error(), "ics[team]") {
		t.Errorf("err = %q", err)
	}
}
