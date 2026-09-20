// Package syncengine implements the offline-first sync protocol: an incremental pull keyed by a
// per-trip sequence number and a push of client mutations that is idempotent and never overwrites
// a newer server version silently. It is transport-agnostic; the HTTP layer only decodes and
// encodes these types.
package syncengine

import (
	"context"
	"encoding/json"
	"time"

	"rinotravel-api/internal/trip"
	"rinotravel-api/internal/user"
)

const (
	DefaultLimit = 200
	MaxLimit     = 500
	MaxMutations = 100
	// CursorMaxAge must stay well below the tombstone retention (90 days): an older cursor could
	// have missed deletions, so the client is told to start over.
	CursorMaxAge = 60 * 24 * time.Hour
)

type Op string

const (
	OpUpsert Op = "upsert"
	OpDelete Op = "delete"
)

type Change struct {
	Entity  string `json:"entity"`
	ID      string `json:"id"`
	Op      Op     `json:"op"`
	Version int64  `json:"version"`
	Record  any    `json:"record,omitempty"`
	// Seq orders changes inside the engine and is not part of the wire format.
	Seq int64 `json:"-"`
}

type Operation string

const (
	OpCreate Operation = "CREATE"
	OpUpdate Operation = "UPDATE"
	OpRemove Operation = "DELETE"
)

type Mutation struct {
	MutationID  string    `json:"mutationId"`
	Entity      string    `json:"entity"`
	EntityID    string    `json:"entityId"`
	Operation   Operation `json:"operation"`
	BaseVersion *int64    `json:"baseVersion"`
	// ClientTimestamp is informational only: device clocks are never trusted to order writes.
	ClientTimestamp *time.Time      `json:"clientTimestamp"`
	Payload         json.RawMessage `json:"payload"`
}

type Status string

const (
	StatusApplied   Status = "applied"
	StatusDuplicate Status = "duplicate"
	StatusConflict  Status = "conflict"
	StatusRejected  Status = "rejected"
)

type Result struct {
	MutationID string `json:"mutationId"`
	Status     Status `json:"status"`
	Code       string `json:"code,omitempty"`
	Message    string `json:"message,omitempty"`
	Version    int64  `json:"version,omitempty"`
	// Record is the entity after an applied write, or the server's current copy on a conflict.
	Record any `json:"record,omitempty"`
}

// Outcome is what a Source reports for an applied mutation.
type Outcome struct {
	Version int64
	Record  any
}

// Source is one syncable entity type. Apply must go through the same use cases as the REST API so
// sync can never bypass validation or authorization.
type Source interface {
	Name() string
	Changes(ctx context.Context, actor user.ID, tripID string, afterSeq int64, limit int, role trip.Role) ([]Change, error)
	Current(ctx context.Context, actor user.ID, tripID, id string, role trip.Role) (Change, bool, error)
	Apply(ctx context.Context, actor user.ID, tripID trip.ID, m Mutation) (Outcome, error)
}

type MutationRecord struct {
	UserID     string
	MutationID string
	TripID     string
	Hash       string
	Result     Result
	CreatedAt  time.Time
}

type MutationLog interface {
	Find(ctx context.Context, userID, mutationID string) (MutationRecord, bool, error)
	// Save must fail with ErrDuplicateMutation when the (user, mutation) pair already exists.
	Save(ctx context.Context, record MutationRecord) error
}

type Transactor interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}

type PullResult struct {
	Changes       []Change  `json:"changes"`
	Cursor        string    `json:"cursor"`
	HasMore       bool      `json:"hasMore"`
	ResetRequired bool      `json:"resetRequired"`
	ServerTime    time.Time `json:"serverTime"`
}
