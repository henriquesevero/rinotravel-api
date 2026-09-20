package mongorepo

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
	"rinotravel-api/internal/trip"
	"rinotravel-api/internal/user"
)

const collectionName = "trips"

type Repository struct {
	db    *mongo.Database
	trips *mongo.Collection
}

func New(db *mongo.Database) *Repository {
	return &Repository{db: db, trips: db.Collection(collectionName)}
}

type document struct {
	ID          string           `bson:"_id"`
	Name        string           `bson:"name"`
	Destination string           `bson:"destination"`
	StartDate   string           `bson:"startDate"`
	EndDate     string           `bson:"endDate"`
	Timezone    string           `bson:"timezone"`
	Currency    string           `bson:"currency"`
	Members     []memberDocument `bson:"members"`
	Version     int64            `bson:"version"`
	Seq         int64            `bson:"seq"`
	CreatedAt   time.Time        `bson:"createdAt"`
	UpdatedAt   time.Time        `bson:"updatedAt"`
	DeletedAt   *time.Time       `bson:"deletedAt,omitempty"`
}

type memberDocument struct {
	UserID    string    `bson:"userId"`
	Role      string    `bson:"role"`
	CreatedAt time.Time `bson:"createdAt"`
	UpdatedAt time.Time `bson:"updatedAt"`
}

func (r *Repository) EnsureIndexes(ctx context.Context) error {
	_, err := r.trips.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "members.userId", Value: 1}, {Key: "startDate", Value: -1}},
	})
	if err != nil {
		return fmt.Errorf("create trips indexes: %w", err)
	}
	return nil
}

func (r *Repository) Create(ctx context.Context, t trip.Trip) error {
	return mongodb.WithTransaction(ctx, r.db, func(ctx context.Context) error {
		seq, err := mongodb.NextSeq(ctx, r.db, string(t.ID))
		if err != nil {
			return err
		}
		doc := toDocument(t)
		doc.Seq = seq

		_, err = r.trips.InsertOne(ctx, doc)
		if mongo.IsDuplicateKeyError(err) {
			return trip.ErrAlreadyExists
		}
		if err != nil {
			return fmt.Errorf("insert trip: %w", err)
		}
		return nil
	})
}

func (r *Repository) Update(ctx context.Context, t trip.Trip) error {
	return mongodb.WithTransaction(ctx, r.db, func(ctx context.Context) error {
		seq, err := mongodb.NextSeq(ctx, r.db, string(t.ID))
		if err != nil {
			return err
		}
		doc := toDocument(t)
		doc.Seq = seq

		filter := bson.D{
			{Key: "_id", Value: string(t.ID)},
			{Key: "version", Value: t.Version - 1},
			{Key: "deletedAt", Value: nil},
		}
		result, err := r.trips.ReplaceOne(ctx, filter, doc)
		if err != nil {
			return fmt.Errorf("replace trip: %w", err)
		}
		if result.MatchedCount == 1 {
			return nil
		}

		live, err := r.trips.CountDocuments(ctx, bson.D{{Key: "_id", Value: string(t.ID)}, {Key: "deletedAt", Value: nil}})
		if err != nil {
			return fmt.Errorf("count trip: %w", err)
		}
		if live == 0 {
			return trip.ErrNotFound
		}
		return trip.ErrVersionConflict
	})
}

func (r *Repository) FindByID(ctx context.Context, id trip.ID) (trip.Trip, error) {
	var doc document
	err := r.trips.FindOne(ctx, bson.D{{Key: "_id", Value: string(id)}, {Key: "deletedAt", Value: nil}}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return trip.Trip{}, trip.ErrNotFound
	}
	if err != nil {
		return trip.Trip{}, fmt.Errorf("find trip: %w", err)
	}
	return doc.toTrip(), nil
}

func (r *Repository) ListByMember(ctx context.Context, userID user.ID) ([]trip.Trip, error) {
	cursor, err := r.trips.Find(ctx,
		bson.D{{Key: "members.userId", Value: string(userID)}, {Key: "deletedAt", Value: nil}},
		options.Find().SetSort(bson.D{{Key: "startDate", Value: -1}, {Key: "_id", Value: 1}}),
	)
	if err != nil {
		return nil, fmt.Errorf("find trips: %w", err)
	}
	var docs []document
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("decode trips: %w", err)
	}

	trips := make([]trip.Trip, 0, len(docs))
	for _, doc := range docs {
		trips = append(trips, doc.toTrip())
	}
	return trips, nil
}

func toDocument(t trip.Trip) document {
	members := make([]memberDocument, 0, len(t.Members))
	for _, m := range t.Members {
		members = append(members, memberDocument{
			UserID:    string(m.UserID),
			Role:      string(m.Role),
			CreatedAt: m.CreatedAt,
			UpdatedAt: m.UpdatedAt,
		})
	}
	return document{
		ID:          string(t.ID),
		Name:        t.Name,
		Destination: t.Destination,
		StartDate:   string(t.StartDate),
		EndDate:     string(t.EndDate),
		Timezone:    string(t.Timezone),
		Currency:    string(t.Currency),
		Members:     members,
		Version:     t.Version,
		CreatedAt:   t.CreatedAt,
		UpdatedAt:   t.UpdatedAt,
		DeletedAt:   t.DeletedAt,
	}
}

func (d document) toTrip() trip.Trip {
	members := make([]trip.Member, 0, len(d.Members))
	for _, m := range d.Members {
		members = append(members, trip.Member{
			UserID:    user.ID(m.UserID),
			Role:      trip.Role(m.Role),
			CreatedAt: m.CreatedAt.UTC(),
			UpdatedAt: m.UpdatedAt.UTC(),
		})
	}
	var deletedAt *time.Time
	if d.DeletedAt != nil {
		utc := d.DeletedAt.UTC()
		deletedAt = &utc
	}
	return trip.Trip{
		ID: trip.ID(d.ID),
		Details: trip.Details{
			Name:        d.Name,
			Destination: d.Destination,
			StartDate:   kernel.Date(d.StartDate),
			EndDate:     kernel.Date(d.EndDate),
			Timezone:    kernel.Timezone(d.Timezone),
			Currency:    kernel.Currency(d.Currency),
		},
		Members:   members,
		Version:   d.Version,
		CreatedAt: d.CreatedAt.UTC(),
		UpdatedAt: d.UpdatedAt.UTC(),
		DeletedAt: deletedAt,
	}
}
