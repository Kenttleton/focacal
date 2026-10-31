package store

import (
	"context"
	"database/sql"
)

// DeduplicateByUID removes duplicate events that share the same UID across
// multiple sources. When the same meeting appears in more than one source
// (e.g. a Google Meet invite accepted in both Google Calendar and iCloud),
// all copies are deleted except the one with the most recent synced_at.
// Returns the number of rows deleted.
func (db *DB) DeduplicateByUID(ctx context.Context) (int, error) {
	// Two-step approach compatible with SQLite's limited DELETE syntax.
	//
	// Step 1: find all UIDs that appear in more than one source.
	// Step 2: for each such UID, delete all rows except the one with MAX(synced_at).
	const q = `
DELETE FROM events
WHERE uid IN (
    SELECT uid FROM events
    GROUP BY uid
    HAVING COUNT(DISTINCT source_id) > 1
)
AND synced_at < (
    SELECT MAX(e2.synced_at)
    FROM events e2
    WHERE e2.uid = events.uid
)`

	res, err := db.ExecContext(ctx, q)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
