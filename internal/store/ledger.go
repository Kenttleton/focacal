package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"html"
	"regexp"
	"strings"
	"time"
)

// LedgerEntry records one remote event focacal wrote. CalendarID is the
// target calendar (the connector's source id, which is what ingest sees as
// Event.SourceID), RemoteID the id ingest will later store as Event.UID, and
// ItemKey the block or event id the write came from.
type LedgerEntry struct {
	CalendarID    string
	RemoteID      string
	DestinationID string
	ItemKey       string
	Hash          string
	// HashTrunc is the hash with the body cut to BodyPreviewRunes, for
	// providers that store only a body preview (Outlook).
	HashTrunc string
	// StartsAt is the written event's start: the field the push window
	// filters on, so it gates remote deletes of items missing from the
	// projection. EndsAt decides when the row is forgotten.
	StartsAt  time.Time
	EndsAt    time.Time
	WrittenAt time.Time
}

// BodyPreviewRunes is the preview length some providers keep of a body.
const BodyPreviewRunes = 255

var (
	tagRe   = regexp.MustCompile(`(?s)<[^>]*>`)
	breakRe = regexp.MustCompile(`(?i)<\s*(br|/p|/div|/li)\s*/?>`)
)

// FieldHash hashes the fields focacal writes to a remote event, normalized
// because remotes rewrite whitespace, markup and time zones: a trimmed,
// whitespace-collapsed title; a body reduced to plain text (tags dropped,
// entities decoded, whitespace collapsed); start and end in UTC at second
// precision.
func FieldHash(title, body string, start, end time.Time) string {
	return fieldHash(title, plainText(body), start, end)
}

// FieldHashTrunc is FieldHash over the plain-text body cut to
// BodyPreviewRunes runes.
func FieldHashTrunc(title, body string, start, end time.Time) string {
	r := []rune(plainText(body))
	if len(r) > BodyPreviewRunes {
		r = r[:BodyPreviewRunes]
	}
	return fieldHash(title, string(r), start, end)
}

func plainText(body string) string {
	body = breakRe.ReplaceAllString(body, " ")
	body = tagRe.ReplaceAllString(body, "")
	return strings.Join(strings.Fields(html.UnescapeString(body)), " ")
}

func fieldHash(title, plain string, start, end time.Time) string {
	h := sha256.New()
	for _, part := range []string{
		strings.Join(strings.Fields(title), " "),
		strings.Join(strings.Fields(plain), " "),
		start.UTC().Truncate(time.Second).Format(time.RFC3339),
		end.UTC().Truncate(time.Second).Format(time.RFC3339),
	} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// LedgerGet returns the hash focacal last wrote for a remote event.
func (db *DB) LedgerGet(ctx context.Context, calendarID, remoteID string) (hash string, ok bool, err error) {
	err = db.QueryRowContext(ctx,
		`SELECT hash FROM write_ledger WHERE calendar_id = ? AND remote_id = ?`,
		calendarID, remoteID).Scan(&hash)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return hash, true, nil
}

// LedgerHashes returns both hashes recorded for a remote event.
func (db *DB) LedgerHashes(ctx context.Context, calendarID, remoteID string) (hash, hashTrunc string, ok bool, err error) {
	err = db.QueryRowContext(ctx,
		`SELECT hash, hash_trunc FROM write_ledger WHERE calendar_id = ? AND remote_id = ?`,
		calendarID, remoteID).Scan(&hash, &hashTrunc)
	if err == sql.ErrNoRows {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return hash, hashTrunc, true, nil
}

// LedgerPut records a write. It replaces any row with the same (calendar,
// remote id) or the same (destination, item key).
func (db *DB) LedgerPut(ctx context.Context, e LedgerEntry) error {
	if e.WrittenAt.IsZero() {
		e.WrittenAt = time.Now()
	}
	_, err := db.ExecContext(ctx, `
INSERT OR REPLACE INTO write_ledger
	(calendar_id, remote_id, destination_id, item_key, hash, hash_trunc, starts_at, ends_at, written_at)
VALUES (?,?,?,?,?,?,?,?,?)`,
		e.CalendarID, e.RemoteID, e.DestinationID, e.ItemKey, e.Hash, e.HashTrunc, e.StartsAt.UTC(), e.EndsAt.UTC(), e.WrittenAt.UTC())
	return err
}

// LedgerDelete removes one ledger row.
func (db *DB) LedgerDelete(ctx context.Context, calendarID, remoteID string) error {
	_, err := db.ExecContext(ctx,
		`DELETE FROM write_ledger WHERE calendar_id = ? AND remote_id = ?`, calendarID, remoteID)
	return err
}

// LedgerByDestination lists every ledger row a destination wrote.
func (db *DB) LedgerByDestination(ctx context.Context, destinationID string) ([]LedgerEntry, error) {
	rows, err := db.QueryContext(ctx, `
SELECT calendar_id, remote_id, destination_id, item_key, hash, hash_trunc, starts_at, ends_at, written_at
FROM write_ledger WHERE destination_id = ? ORDER BY item_key`, destinationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LedgerEntry
	for rows.Next() {
		var e LedgerEntry
		if err := rows.Scan(&e.CalendarID, &e.RemoteID, &e.DestinationID, &e.ItemKey, &e.Hash, &e.HashTrunc, &e.StartsAt, &e.EndsAt, &e.WrittenAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// LedgerDeleteByCalendar removes every ledger row for a target calendar, used
// when the calendar is removed from config.
func (db *DB) LedgerDeleteByCalendar(ctx context.Context, calendarID string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM write_ledger WHERE calendar_id = ?`, calendarID)
	return err
}
