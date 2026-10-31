package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// BlockType classifies an AI-planned calendar block.
type BlockType string

const (
	BlockFocus  BlockType = "focus"
	BlockLunch  BlockType = "lunch"
	BlockBreak  BlockType = "break"
)

// Block is an AI-planned time block (focus session, lunch, break).
type Block struct {
	ID            string
	Type          BlockType
	Title         string
	Start         time.Time
	End           time.Time
	Priority      int
	Locked        bool              // user-protected; the agent must not delete locked blocks
	SourceEventID string            // non-empty if displaced by a real event
	WrittenBack   map[string]string // source_id -> remote event ID
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// NewBlock creates a Block with a fresh UUID.
func NewBlock(btype BlockType, title string, start, end time.Time, priority int) *Block {
	return &Block{
		ID:          uuid.New().String(),
		Type:        btype,
		Title:       title,
		Start:       start,
		End:         end,
		Priority:    priority,
		WrittenBack: make(map[string]string),
	}
}

// UpsertBlock inserts or replaces a planned block.
func (db *DB) UpsertBlock(ctx context.Context, b *Block) error {
	wb, err := json.Marshal(b.WrittenBack)
	if err != nil {
		return fmt.Errorf("marshal written_back: %w", err)
	}
	const q = `
INSERT INTO blocks (id, type, title, start_time, end_time, priority, locked, source_event_id, written_back, updated_at)
VALUES (?,?,?,?,?,?,?,?,?,CURRENT_TIMESTAMP)
ON CONFLICT(id) DO UPDATE SET
	type=excluded.type, title=excluded.title,
	start_time=excluded.start_time, end_time=excluded.end_time,
	priority=excluded.priority, locked=excluded.locked,
	source_event_id=excluded.source_event_id,
	written_back=excluded.written_back, updated_at=CURRENT_TIMESTAMP`
	_, err = db.ExecContext(ctx, q,
		b.ID, string(b.Type), b.Title,
		b.Start.UTC(), b.End.UTC(),
		b.Priority, b.Locked, b.SourceEventID, string(wb),
	)
	return err
}

// DeleteBlock removes a planned block by ID.
func (db *DB) DeleteBlock(ctx context.Context, id string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM blocks WHERE id = ?`, id)
	return err
}

// DeleteBlocksInRange removes all unlocked planned blocks in [from, to).
// Locked blocks are preserved so user-protected slots survive a replan.
func (db *DB) DeleteBlocksInRange(ctx context.Context, from, to time.Time) error {
	_, err := db.ExecContext(ctx,
		`DELETE FROM blocks WHERE start_time >= ? AND start_time < ? AND locked = 0`,
		from.UTC(), to.UTC(),
	)
	return err
}

// ListBlocks returns all planned blocks in [from, to).
func (db *DB) ListBlocks(ctx context.Context, from, to time.Time) ([]*Block, error) {
	const q = `
SELECT id, type, title, start_time, end_time, priority, locked, source_event_id, written_back, created_at, updated_at
FROM blocks
WHERE start_time >= ? AND start_time < ?
ORDER BY start_time`

	rows, err := db.QueryContext(ctx, q, from.UTC(), to.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var blocks []*Block
	for rows.Next() {
		var b Block
		var wbJSON string
		if err := rows.Scan(
			&b.ID, &b.Type, &b.Title, &b.Start, &b.End,
			&b.Priority, &b.Locked, &b.SourceEventID, &wbJSON,
			&b.CreatedAt, &b.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan block: %w", err)
		}
		b.WrittenBack = make(map[string]string)
		if err := json.Unmarshal([]byte(wbJSON), &b.WrittenBack); err != nil {
			return nil, fmt.Errorf("unmarshal written_back: %w", err)
		}
		blocks = append(blocks, &b)
	}
	return blocks, rows.Err()
}

// GetBlock returns a single block by ID.
func (db *DB) GetBlock(ctx context.Context, id string) (*Block, error) {
	const q = `
SELECT id, type, title, start_time, end_time, priority, locked, source_event_id, written_back, created_at, updated_at
FROM blocks WHERE id = ?`

	var b Block
	var wbJSON string
	err := db.QueryRowContext(ctx, q, id).Scan(
		&b.ID, &b.Type, &b.Title, &b.Start, &b.End,
		&b.Priority, &b.Locked, &b.SourceEventID, &wbJSON,
		&b.CreatedAt, &b.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	b.WrittenBack = make(map[string]string)
	if err := json.Unmarshal([]byte(wbJSON), &b.WrittenBack); err != nil {
		return nil, fmt.Errorf("unmarshal written_back: %w", err)
	}
	return &b, nil
}
