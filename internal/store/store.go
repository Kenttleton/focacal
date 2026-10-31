package store

import (
	"context"
	"time"
)

// Store is the data-access interface for focacal.
//
// Implementations decide internally how to scope data — single-user SQLite
// implementations ignore the concept entirely; multi-tenant PostgreSQL
// implementations bake a userID into the struct at construction time.
// No userID parameter ever appears here.
type Store interface {
	// Events

	UpsertEvent(ctx context.Context, e *Event) error
	DeleteEvent(ctx context.Context, id string) error
	ListEvents(ctx context.Context, from, to time.Time) ([]*Event, error)
	ListEventsBySource(ctx context.Context, sourceID string, from, to time.Time) ([]*Event, error)
	DeduplicateByUID(ctx context.Context) (int, error)

	// Blocks

	UpsertBlock(ctx context.Context, b *Block) error
	GetBlock(ctx context.Context, id string) (*Block, error)
	ListBlocks(ctx context.Context, from, to time.Time) ([]*Block, error)
	DeleteBlock(ctx context.Context, id string) error
	DeleteBlocksInRange(ctx context.Context, from, to time.Time) error

	// Sync state & OAuth — keyed by sourceID within the store's scope

	GetSyncState(ctx context.Context, sourceID string) (token, webhookID string, webhookExpiry time.Time, err error)
	SetSyncState(ctx context.Context, sourceID, token, webhookID string, webhookExpiry time.Time) error
	GetOAuthToken(ctx context.Context, sourceID string) (string, error)
	SetOAuthToken(ctx context.Context, sourceID, tokenJSON string) error
	ListSyncStates(ctx context.Context) ([]SyncStateRow, error)

	// Maintenance

	WALCheckpoint(ctx context.Context) error
}
