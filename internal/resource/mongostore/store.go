// Package mongostore is the MongoDB implementation of resource.Repo shared by every trip-scoped
// entity. Each write runs in a transaction together with the allocation of the trip's next change
// sequence number, which is what makes seq a safe cursor for sync clients.
package mongostore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/platform/mongodb"
	"rinotravel-api/internal/resource"
)

// TombstoneRetention is how long a deleted entity stays available to sync clients before the
// database removes it. A client offline for longer must do a full resync.
const TombstoneRetention = 90 * 24 * time.Hour

type Codec[T any] struct {
	Base func(*T) *kernel.Base
	// Encode returns only the entity's own fields; the envelope (id, tripId, version, seq,
	// timestamps, tombstone) is handled by the store.
	Encode func(T) bson.D
	Decode func(raw bson.Raw, base kernel.Base) (T, error)
}

type Store[T any] struct {
	db    *mongo.Database
	coll  *mongo.Collection
	codec Codec[T]
}

func New[T any](db *mongo.Database, collection string, codec Codec[T]) *Store[T] {
	return &Store[T]{db: db, coll: db.Collection(collection), codec: codec}
}

type envelope struct {
	ID        string     `bson:"_id"`
	TripID    string     `bson:"tripId"`
	Version   int64      `bson:"version"`
	Seq       int64      `bson:"seq"`
	CreatedAt time.Time  `bson:"createdAt"`
	UpdatedAt time.Time  `bson:"updatedAt"`
	DeletedAt *time.Time `bson:"deletedAt"`
}

// UniqueAmongLive is a unique index per trip that ignores tombstones, so a deleted entity frees its key.
func UniqueAmongLive(keys ...bson.E) mongo.IndexModel {
	all := append(bson.D{{Key: "tripId", Value: 1}}, keys...)
	return mongo.IndexModel{
		Keys: all,
		Options: options.Index().SetUnique(true).
			SetPartialFilterExpression(bson.D{{Key: "live", Value: bson.D{{Key: "$exists", Value: true}}}}),
	}
}

func (s *Store[T]) EnsureIndexes(ctx context.Context, extra ...mongo.IndexModel) error {
	models := append([]mongo.IndexModel{
		{Keys: bson.D{{Key: "tripId", Value: 1}, {Key: "seq", Value: 1}}},
		{Keys: bson.D{{Key: "purgeAt", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0)},
	}, extra...)
	if _, err := s.coll.Indexes().CreateMany(ctx, models); err != nil {
		return fmt.Errorf("create %s indexes: %w", s.coll.Name(), err)
	}
	return nil
}

func (s *Store[T]) encode(entity T, seq int64) bson.D {
	b := s.codec.Base(&entity)
	doc := bson.D{
		{Key: "_id", Value: b.ID},
		{Key: "tripId", Value: b.TripID},
		{Key: "version", Value: b.Version},
		{Key: "seq", Value: seq},
		{Key: "createdAt", Value: b.CreatedAt},
		{Key: "updatedAt", Value: b.UpdatedAt},
	}
	if b.DeletedAt == nil {
		// Partial indexes cannot express "field missing", so live documents carry an explicit marker.
		doc = append(doc, bson.E{Key: "live", Value: true})
	} else {
		doc = append(doc,
			bson.E{Key: "deletedAt", Value: *b.DeletedAt},
			bson.E{Key: "purgeAt", Value: b.DeletedAt.Add(TombstoneRetention)},
		)
	}
	return append(doc, s.codec.Encode(entity)...)
}

func (s *Store[T]) decode(raw bson.Raw) (T, error) {
	var env envelope
	if err := bson.Unmarshal(raw, &env); err != nil {
		var zero T
		return zero, fmt.Errorf("decode %s envelope: %w", s.coll.Name(), err)
	}
	base := kernel.Base{
		ID:        env.ID,
		TripID:    env.TripID,
		Version:   env.Version,
		CreatedAt: env.CreatedAt.UTC(),
		UpdatedAt: env.UpdatedAt.UTC(),
	}
	if env.DeletedAt != nil {
		deletedAt := env.DeletedAt.UTC()
		base.DeletedAt = &deletedAt
	}
	return s.codec.Decode(raw, base)
}

func (s *Store[T]) Insert(ctx context.Context, entity T) error {
	tripID := s.codec.Base(&entity).TripID
	return mongodb.WithTransaction(ctx, s.db, func(ctx context.Context) error {
		seq, err := mongodb.NextSeq(ctx, s.db, tripID)
		if err != nil {
			return err
		}
		_, err = s.coll.InsertOne(ctx, s.encode(entity, seq))
		if mongo.IsDuplicateKeyError(err) {
			return kernel.ErrAlreadyExists
		}
		if err != nil {
			return fmt.Errorf("insert %s: %w", s.coll.Name(), err)
		}
		return nil
	})
}

func (s *Store[T]) Replace(ctx context.Context, entity T) error {
	b := *s.codec.Base(&entity)
	return mongodb.WithTransaction(ctx, s.db, func(ctx context.Context) error {
		seq, err := mongodb.NextSeq(ctx, s.db, b.TripID)
		if err != nil {
			return err
		}
		filter := bson.D{
			{Key: "_id", Value: b.ID},
			{Key: "tripId", Value: b.TripID},
			{Key: "version", Value: b.Version - 1},
			{Key: "deletedAt", Value: nil},
		}
		result, err := s.coll.ReplaceOne(ctx, filter, s.encode(entity, seq))
		if mongo.IsDuplicateKeyError(err) {
			return kernel.ErrAlreadyExists
		}
		if err != nil {
			return fmt.Errorf("replace %s: %w", s.coll.Name(), err)
		}
		if result.MatchedCount == 1 {
			return nil
		}

		live, err := s.coll.CountDocuments(ctx, bson.D{
			{Key: "_id", Value: b.ID}, {Key: "tripId", Value: b.TripID}, {Key: "deletedAt", Value: nil},
		})
		if err != nil {
			return fmt.Errorf("count %s: %w", s.coll.Name(), err)
		}
		if live == 0 {
			return kernel.ErrNotFound
		}
		return kernel.ErrVersionConflict
	})
}

func (s *Store[T]) findOne(ctx context.Context, filter bson.D) (T, error) {
	var zero T
	raw, err := s.coll.FindOne(ctx, filter).Raw()
	if errors.Is(err, mongo.ErrNoDocuments) {
		return zero, kernel.ErrNotFound
	}
	if err != nil {
		return zero, fmt.Errorf("find %s: %w", s.coll.Name(), err)
	}
	return s.decode(raw)
}

func (s *Store[T]) Get(ctx context.Context, tripID, id string) (T, error) {
	return s.findOne(ctx, bson.D{{Key: "_id", Value: id}, {Key: "tripId", Value: tripID}, {Key: "deletedAt", Value: nil}})
}

func (s *Store[T]) GetAny(ctx context.Context, tripID, id string) (T, error) {
	return s.findOne(ctx, bson.D{{Key: "_id", Value: id}, {Key: "tripId", Value: tripID}})
}

func (s *Store[T]) decodeAll(ctx context.Context, cursor *mongo.Cursor) ([]T, error) {
	defer cursor.Close(ctx)
	var out []T
	for cursor.Next(ctx) {
		entity, err := s.decode(cursor.Current)
		if err != nil {
			return nil, err
		}
		out = append(out, entity)
	}
	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", s.coll.Name(), err)
	}
	return out, nil
}

func (s *Store[T]) List(ctx context.Context, tripID string) ([]T, error) {
	cursor, err := s.coll.Find(ctx,
		bson.D{{Key: "tripId", Value: tripID}, {Key: "deletedAt", Value: nil}},
		options.Find().SetSort(bson.D{{Key: "createdAt", Value: 1}, {Key: "_id", Value: 1}}),
	)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", s.coll.Name(), err)
	}
	return s.decodeAll(ctx, cursor)
}

func (s *Store[T]) Changes(ctx context.Context, tripID string, afterSeq int64, limit int) ([]resource.Change[T], error) {
	filter := bson.D{{Key: "tripId", Value: tripID}, {Key: "seq", Value: bson.D{{Key: "$gt", Value: afterSeq}}}}
	if afterSeq == 0 {
		filter = append(filter, bson.E{Key: "deletedAt", Value: nil})
	}
	opts := options.Find().SetSort(bson.D{{Key: "seq", Value: 1}})
	if limit > 0 {
		opts.SetLimit(int64(limit))
	}
	cursor, err := s.coll.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("changes %s: %w", s.coll.Name(), err)
	}
	defer cursor.Close(ctx)

	var out []resource.Change[T]
	for cursor.Next(ctx) {
		var env envelope
		if err := bson.Unmarshal(cursor.Current, &env); err != nil {
			return nil, fmt.Errorf("decode %s envelope: %w", s.coll.Name(), err)
		}
		entity, err := s.decode(cursor.Current)
		if err != nil {
			return nil, err
		}
		out = append(out, resource.Change[T]{Seq: env.Seq, Entity: entity})
	}
	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", s.coll.Name(), err)
	}
	return out, nil
}
