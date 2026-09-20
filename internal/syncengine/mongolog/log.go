// Package mongolog stores applied mutations so a retried mutationId is answered from the log
// instead of being applied twice.
package mongolog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"rinotravel-api/internal/platform/mongodb"
	"rinotravel-api/internal/syncengine"
)

// Retention is how long a client may retry a mutation and still get the recorded answer.
const Retention = 30 * 24 * time.Hour

type Log struct {
	db   *mongo.Database
	coll *mongo.Collection
}

func New(db *mongo.Database) *Log {
	return &Log{db: db, coll: db.Collection("sync_mutations")}
}

type document struct {
	ID         string    `bson:"_id"`
	UserID     string    `bson:"userId"`
	MutationID string    `bson:"mutationId"`
	TripID     string    `bson:"tripId"`
	Hash       string    `bson:"hash"`
	Result     string    `bson:"result"`
	CreatedAt  time.Time `bson:"createdAt"`
	ExpireAt   time.Time `bson:"expireAt"`
}

func (l *Log) EnsureIndexes(ctx context.Context) error {
	_, err := l.coll.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "expireAt", Value: 1}},
		Options: options.Index().SetExpireAfterSeconds(0),
	})
	if err != nil {
		return fmt.Errorf("create sync_mutations indexes: %w", err)
	}
	return nil
}

func key(userID, mutationID string) string { return userID + "/" + mutationID }

func (l *Log) Find(ctx context.Context, userID, mutationID string) (syncengine.MutationRecord, bool, error) {
	var doc document
	err := l.coll.FindOne(ctx, bson.D{{Key: "_id", Value: key(userID, mutationID)}}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return syncengine.MutationRecord{}, false, nil
	}
	if err != nil {
		return syncengine.MutationRecord{}, false, fmt.Errorf("find mutation: %w", err)
	}
	var result syncengine.Result
	if err := json.Unmarshal([]byte(doc.Result), &result); err != nil {
		return syncengine.MutationRecord{}, false, fmt.Errorf("decode recorded result: %w", err)
	}
	return syncengine.MutationRecord{
		UserID: doc.UserID, MutationID: doc.MutationID, TripID: doc.TripID,
		Hash: doc.Hash, Result: result, CreatedAt: doc.CreatedAt.UTC(),
	}, true, nil
}

func (l *Log) Save(ctx context.Context, r syncengine.MutationRecord) error {
	encoded, err := json.Marshal(r.Result)
	if err != nil {
		return fmt.Errorf("encode result: %w", err)
	}
	_, err = l.coll.InsertOne(ctx, document{
		ID: key(r.UserID, r.MutationID), UserID: r.UserID, MutationID: r.MutationID, TripID: r.TripID,
		Hash: r.Hash, Result: string(encoded), CreatedAt: r.CreatedAt, ExpireAt: r.CreatedAt.Add(Retention),
	})
	if mongo.IsDuplicateKeyError(err) {
		return syncengine.ErrDuplicateMutation
	}
	if err != nil {
		return fmt.Errorf("save mutation: %w", err)
	}
	return nil
}

// Transactor runs a mutation and its log entry atomically.
type Transactor struct{ db *mongo.Database }

func NewTransactor(db *mongo.Database) Transactor { return Transactor{db: db} }

func (t Transactor) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	return mongodb.WithTransaction(ctx, t.db, fn)
}
