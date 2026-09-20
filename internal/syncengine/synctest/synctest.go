// Package synctest has in-memory stand-ins for the sync engine's log and transactor.
package synctest

import (
	"context"
	"sync"

	"rinotravel-api/internal/syncengine"
)

type Log struct {
	mu      sync.Mutex
	Records map[string]syncengine.MutationRecord
}

func NewLog() *Log { return &Log{Records: map[string]syncengine.MutationRecord{}} }

func (l *Log) Find(_ context.Context, userID, id string) (syncengine.MutationRecord, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	r, ok := l.Records[userID+"/"+id]
	return r, ok, nil
}

func (l *Log) Save(_ context.Context, r syncengine.MutationRecord) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := r.UserID + "/" + r.MutationID
	if _, exists := l.Records[key]; exists {
		return syncengine.ErrDuplicateMutation
	}
	l.Records[key] = r
	return nil
}

// Direct runs the function without a real transaction.
type Direct struct{}

func (Direct) Do(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }
