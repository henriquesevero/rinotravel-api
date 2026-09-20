// Package mongorepo keeps the monthly quota counters in MongoDB, one document per bucket and month.
package mongorepo

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"rinotravel-api/internal/platform/clock"
	"rinotravel-api/internal/quota"
)

type Counter struct {
	coll *mongo.Collection
	now  func() time.Time
}

func New(db *mongo.Database) *Counter {
	return &Counter{coll: db.Collection("provider_usage"), now: clock.Now}
}

// NewWithClock lets tests move between months.
func NewWithClock(db *mongo.Database, now func() time.Time) *Counter {
	return &Counter{coll: db.Collection("provider_usage"), now: now}
}

// Take is a single atomic upsert. The filter only matches while the count is below the limit, so
// once it is reached the upsert tries to insert a document with the same _id and Mongo answers with
// a duplicate-key error: that is the "exhausted" signal, and it needs no transaction or lock.
//
// The month is a UTC calendar month. Google bills by its own month boundary, so the limit should
// keep a margin below the free allowance rather than rely on the exact cutover.
func (c *Counter) Take(ctx context.Context, bucket string, limit int) (int, error) {
	month := c.now().UTC().Format("2006-01")
	id := bucket + ":" + month

	var doc struct {
		Count int `bson:"count"`
	}
	err := c.coll.FindOneAndUpdate(ctx,
		bson.M{"_id": id, "count": bson.M{"$lt": limit}},
		bson.M{
			"$inc":         bson.M{"count": 1},
			"$setOnInsert": bson.M{"bucket": bucket, "month": month},
		},
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After),
	).Decode(&doc)
	switch {
	case err == nil:
		return doc.Count, nil
	case mongo.IsDuplicateKeyError(err):
		return limit, quota.ErrExhausted
	default:
		return 0, fmt.Errorf("take from quota %s: %w", id, err)
	}
}
