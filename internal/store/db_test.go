package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func userVersion(t *testing.T, db *sql.DB) int {
	t.Helper()
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func count(t *testing.T, db *sql.DB, q string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func seedV1(t *testing.T, path string) {
	t.Helper()
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`INSERT INTO events (id, source_id, calendar_id, uid, start_time, end_time) VALUES ('s:c:1','s','c','1','2026-01-01T09:00:00Z','2026-01-01T10:00:00Z')`,
		`INSERT INTO sync_state (source_id, sync_token) VALUES ('s','tok')`,
		`INSERT INTO blocks (id, type, title, start_time, end_time, source_event_id, written_back) VALUES ('b1','focus','Focus','2026-01-01T11:00:00Z','2026-01-01T12:00:00Z','s:c:1','{"s":"r1"}')`,
		`INSERT INTO oauth_tokens (source_id, token_json) VALUES ('s','{"a":1}')`,
		`PRAGMA user_version = 1`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

func TestStoreVersion2ResetsCaches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	seedV1(t, path)

	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if v := userVersion(t, db.DB); v != 2 {
		t.Errorf("user_version = %d, want 2", v)
	}
	if n := count(t, db.DB, `SELECT COUNT(*) FROM events`); n != 0 {
		t.Errorf("events = %d", n)
	}
	if n := count(t, db.DB, `SELECT COUNT(*) FROM sync_state`); n != 0 {
		t.Errorf("sync_state = %d", n)
	}
	if n := count(t, db.DB, `SELECT COUNT(*) FROM blocks WHERE id='b1' AND source_event_id IS NULL AND written_back='{}'`); n != 1 {
		t.Errorf("block not kept/cleared")
	}
	if n := count(t, db.DB, `SELECT COUNT(*) FROM oauth_tokens`); n != 1 {
		t.Errorf("oauth token lost")
	}
	// New data after the reset survives a reopen: the reset runs once.
	if _, err := db.Exec(`INSERT INTO sync_state (source_id) VALUES ('new')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE blocks SET source_event_id='x', written_back='{"a":"b"}'`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if n := count(t, db.DB, `SELECT COUNT(*) FROM sync_state`); n != 1 {
		t.Errorf("reset repeated on reopen: sync_state = %d", n)
	}
	if n := count(t, db.DB, `SELECT COUNT(*) FROM blocks WHERE source_event_id='x'`); n != 1 {
		t.Error("reset repeated on reopen: blocks cleared")
	}
}

func TestStoreVersion0ResetsAndFreshEndsAtV2(t *testing.T) {
	// Pre-versioning database (user_version 0) with data resets.
	path := filepath.Join(t.TempDir(), "old.db")
	seedV1(t, path)
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	raw.Exec(`PRAGMA user_version = 0`)
	raw.Close()
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := count(t, db.DB, `SELECT COUNT(*) FROM events`); n != 0 || userVersion(t, db.DB) != 2 {
		t.Errorf("v0 did not reset to v2")
	}
	db.Close()

	// A fresh database ends at version 2 and takes writes normally.
	fresh, err := Open(filepath.Join(t.TempDir(), "new.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if v := userVersion(t, fresh.DB); v != 2 {
		t.Errorf("fresh user_version = %d, want 2", v)
	}
}
