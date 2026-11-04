package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Event is a unified calendar event from any source.
type Event struct {
	ID             string
	SourceID       string
	CalendarID     string
	UID            string
	ICalUID        string
	Title          string
	Description    string
	Location       string
	Start          time.Time
	End            time.Time
	AllDay         bool
	Status         string // confirmed | tentative | cancelled
	ResponseStatus string // accepted | tentative | declined | needs_action
	ShowAs         string // busy | free | oof (empty treated as busy)
	ConferenceURL  string
	RawICal        string
	SyncedAt       time.Time
}

// EventID builds the composite id for an event row.
func EventID(sourceID, calendarID, uid string) string {
	return sourceID + ":" + calendarID + ":" + uid
}

// IsTentative reports whether the event is tentative.
func (e *Event) IsTentative() bool {
	return e.Status == "tentative" || e.ResponseStatus == "tentative"
}

// eventColumns is the shared column list for events (keep in sync with scanEvents).
const eventColumns = `id, source_id, calendar_id, uid, ical_uid, title, description, location,
	start_time, end_time, all_day, status, response_status, show_as, conference_url, raw_ical, synced_at`

// UpsertEvent inserts or replaces an event by its composite ID.
func (db *DB) UpsertEvent(ctx context.Context, e *Event) error {
	const q = `
INSERT INTO events
	(id, source_id, calendar_id, uid, ical_uid, title, description, location,
	 start_time, end_time, all_day, status, response_status, show_as, conference_url, raw_ical, synced_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
	calendar_id=excluded.calendar_id, uid=excluded.uid, ical_uid=excluded.ical_uid,
	title=excluded.title, description=excluded.description,
	location=excluded.location, start_time=excluded.start_time,
	end_time=excluded.end_time, all_day=excluded.all_day,
	status=excluded.status, response_status=excluded.response_status,
	show_as=excluded.show_as, conference_url=excluded.conference_url,
	raw_ical=excluded.raw_ical, synced_at=excluded.synced_at`

	_, err := db.ExecContext(ctx, q,
		e.ID, e.SourceID, e.CalendarID, e.UID, e.ICalUID, e.Title, e.Description, e.Location,
		e.Start.UTC(), e.End.UTC(), e.AllDay, e.Status, e.ResponseStatus, e.ShowAs, e.ConferenceURL,
		e.RawICal, time.Now().UTC(),
	)
	return err
}

// DeleteEvent removes an event by its composite ID.
func (db *DB) DeleteEvent(ctx context.Context, id string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM events WHERE id = ?`, id)
	return err
}

// DeleteEventsBySource removes every stored event belonging to sourceID and
// returns how many rows were deleted. Used when a source is dropped from config.
func (db *DB) DeleteEventsBySource(ctx context.Context, sourceID string) (int, error) {
	res, err := db.ExecContext(ctx, `DELETE FROM events WHERE source_id = ?`, sourceID)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// ListEvents returns all events whose start_time falls within [from, to).
func (db *DB) ListEvents(ctx context.Context, from, to time.Time) ([]*Event, error) {
	q := `SELECT ` + eventColumns + `
FROM events
WHERE start_time >= ? AND start_time < ?
  AND status != 'cancelled'
ORDER BY start_time`

	rows, err := db.QueryContext(ctx, q, from.UTC(), to.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEvents(rows)
}

// ListEventsByCalendar returns events for a specific source/calendar in the given range.
func (db *DB) ListEventsByCalendar(ctx context.Context, sourceID, calendarID string, from, to time.Time) ([]*Event, error) {
	q := `SELECT ` + eventColumns + `
FROM events
WHERE source_id = ? AND start_time >= ? AND start_time < ?`
	args := []interface{}{sourceID, from.UTC(), to.UTC()}
	if calendarID != "" {
		q += ` AND calendar_id = ?`
		args = append(args, calendarID)
	}
	q += ` ORDER BY start_time`

	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEvents(rows)
}

// DismissConflict records that a conflict key has been seen.
func (db *DB) DismissConflict(ctx context.Context, key string) error {
	_, err := db.ExecContext(ctx, `
INSERT INTO conflict_dismissals (key) VALUES (?)
ON CONFLICT(key) DO NOTHING`, key)
	return err
}

// ListDismissedConflicts returns the dismissed conflict keys with their timestamps.
func (db *DB) ListDismissedConflicts(ctx context.Context) (map[string]time.Time, error) {
	rows, err := db.QueryContext(ctx, `SELECT key, dismissed_at FROM conflict_dismissals`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]time.Time)
	for rows.Next() {
		var k string
		var ts time.Time
		if err := rows.Scan(&k, &ts); err != nil {
			return nil, err
		}
		out[k] = ts
	}
	return out, rows.Err()
}
func (db *DB) GetSyncState(ctx context.Context, sourceID string) (token, webhookID string, webhookExpiry time.Time, err error) {
	const q = `SELECT sync_token, webhook_id, webhook_expiry FROM sync_state WHERE source_id = ?`
	var expiry sql.NullTime
	err = db.QueryRowContext(ctx, q, sourceID).Scan(&token, &webhookID, &expiry)
	if err == sql.ErrNoRows {
		return "", "", time.Time{}, nil
	}
	if expiry.Valid {
		webhookExpiry = expiry.Time
	}
	return
}

// SetSyncState persists incremental sync state for a source.
func (db *DB) SetSyncState(ctx context.Context, sourceID, token, webhookID string, webhookExpiry time.Time) error {
	const q = `
INSERT INTO sync_state (source_id, last_synced_at, sync_token, webhook_id, webhook_expiry)
VALUES (?, CURRENT_TIMESTAMP, ?, ?, ?)
ON CONFLICT(source_id) DO UPDATE SET
	last_synced_at=CURRENT_TIMESTAMP, sync_token=excluded.sync_token,
	webhook_id=excluded.webhook_id, webhook_expiry=excluded.webhook_expiry`
	var expiry interface{}
	if !webhookExpiry.IsZero() {
		expiry = webhookExpiry.UTC()
	}
	_, err := db.ExecContext(ctx, q, sourceID, token, webhookID, expiry)
	return err
}

// GetOAuthToken retrieves a stored OAuth token JSON for a source.
func (db *DB) GetOAuthToken(ctx context.Context, sourceID string) (string, error) {
	var tokenJSON string
	err := db.QueryRowContext(ctx,
		`SELECT token_json FROM oauth_tokens WHERE source_id = ?`, sourceID,
	).Scan(&tokenJSON)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return tokenJSON, err
}

// SetOAuthToken persists an OAuth token JSON for a source.
func (db *DB) SetOAuthToken(ctx context.Context, sourceID, tokenJSON string) error {
	const q = `
INSERT INTO oauth_tokens (source_id, token_json, updated_at)
VALUES (?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(source_id) DO UPDATE SET token_json=excluded.token_json, updated_at=CURRENT_TIMESTAMP`
	_, err := db.ExecContext(ctx, q, sourceID, tokenJSON)
	return err
}

// SyncStateRow is a snapshot of one source's sync and webhook state.
type SyncStateRow struct {
	SourceID      string    `json:"source_id"`
	LastSyncedAt  time.Time `json:"last_synced_at"`
	WebhookID     string    `json:"webhook_id,omitempty"`
	WebhookExpiry time.Time `json:"webhook_expiry,omitempty"`
}

// DeleteSyncStateBySource removes the incremental-sync/webhook state of a
// source: the row keyed by the bare source id (outlook webhook) and the
// per-calendar rows keyed "<id>:<calendar>" (google and outlook sync tokens,
// google webhooks). OAuth tokens are left alone so a re-added source keeps its
// login. Returns the number of rows deleted.
//
// sourceID must not contain ":" (the API rejects such ids): with one, "a" and
// "a:b" would be ambiguous. Callers skip ids that do.
func (db *DB) DeleteSyncStateBySource(ctx context.Context, sourceID string) (int, error) {
	const q = `DELETE FROM sync_state WHERE source_id = ? OR substr(source_id, 1, length(?) + 1) = ? || ':'`
	res, err := db.ExecContext(ctx, q, sourceID, sourceID, sourceID)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// ListSyncStates returns the sync_state row for every known source.
func (db *DB) ListSyncStates(ctx context.Context) ([]SyncStateRow, error) {
	const q = `
SELECT source_id, last_synced_at, webhook_id, webhook_expiry
FROM sync_state ORDER BY source_id`

	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SyncStateRow
	for rows.Next() {
		var r SyncStateRow
		var lastSync sql.NullTime
		var webhookID sql.NullString
		var expiry sql.NullTime
		if err := rows.Scan(&r.SourceID, &lastSync, &webhookID, &expiry); err != nil {
			return nil, err
		}
		if lastSync.Valid {
			r.LastSyncedAt = lastSync.Time
		}
		if webhookID.Valid {
			r.WebhookID = webhookID.String
		}
		if expiry.Valid {
			r.WebhookExpiry = expiry.Time
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanEvents(rows *sql.Rows) ([]*Event, error) {
	var events []*Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(
			&e.ID, &e.SourceID, &e.CalendarID, &e.UID, &e.ICalUID, &e.Title, &e.Description, &e.Location,
			&e.Start, &e.End, &e.AllDay, &e.Status, &e.ResponseStatus, &e.ShowAs, &e.ConferenceURL,
			&e.RawICal, &e.SyncedAt,
		); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		events = append(events, &e)
	}
	return events, rows.Err()
}
