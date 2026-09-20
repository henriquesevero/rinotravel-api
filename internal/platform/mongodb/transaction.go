package mongodb

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

const seqCollection = "sync_counters"

// WithTransaction runs fn atomically. When ctx already carries a transaction, fn joins it, so a
// caller can group several repository writes (each of which opens its own transaction) into one.
func WithTransaction(ctx context.Context, db *mongo.Database, fn func(ctx context.Context) error) error {
	if mongo.SessionFromContext(ctx) != nil {
		return fn(ctx)
	}
	session, err := db.Client().StartSession()
	if err != nil {
		return fmt.Errorf("start mongodb session: %w", err)
	}
	defer session.EndSession(ctx)

	opts := options.Transaction().SetWriteConcern(writeconcern.Majority())
	_, err = session.WithTransaction(ctx, func(ctx context.Context) (any, error) {
		return nil, fn(ctx)
	}, opts)
	return err
}

// NextSeq returns the next change sequence number for a trip. It must run
// inside WithTransaction together with the write it stamps: every write to the
// same trip touches this counter document, so concurrent writers serialize and
// commit order matches seq order. That is what lets sync clients use seq as a
// cursor without missing changes.
func NextSeq(ctx context.Context, db *mongo.Database, tripID string) (int64, error) {
	var counter struct {
		Seq int64 `bson:"seq"`
	}
	err := db.Collection(seqCollection).FindOneAndUpdate(ctx,
		bson.D{{Key: "_id", Value: tripID}},
		bson.D{{Key: "$inc", Value: bson.D{{Key: "seq", Value: 1}}}},
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After),
	).Decode(&counter)
	if err != nil {
		return 0, fmt.Errorf("allocate seq for trip: %w", err)
	}
	return counter.Seq, nil
}
