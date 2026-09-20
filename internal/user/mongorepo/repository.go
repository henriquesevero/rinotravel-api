package mongorepo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"rinotravel-api/internal/user"
)

const collectionName = "users"

type Repository struct {
	users *mongo.Collection
}

func New(db *mongo.Database) *Repository {
	return &Repository{users: db.Collection(collectionName)}
}

type document struct {
	ID           string    `bson:"_id"`
	Email        string    `bson:"email"`
	Name         string    `bson:"name"`
	PasswordHash string    `bson:"passwordHash"`
	CreatedAt    time.Time `bson:"createdAt"`
	UpdatedAt    time.Time `bson:"updatedAt"`
}

func (r *Repository) EnsureIndexes(ctx context.Context) error {
	_, err := r.users.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "email", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return fmt.Errorf("create users indexes: %w", err)
	}
	return nil
}

func (r *Repository) Create(ctx context.Context, u user.User) error {
	_, err := r.users.InsertOne(ctx, toDocument(u))
	if mongo.IsDuplicateKeyError(err) {
		return user.ErrEmailTaken
	}
	if err != nil {
		return fmt.Errorf("insert user: %w", err)
	}
	return nil
}

func (r *Repository) FindByID(ctx context.Context, id user.ID) (user.User, error) {
	return r.findOne(ctx, bson.D{{Key: "_id", Value: string(id)}})
}

func (r *Repository) FindByEmail(ctx context.Context, email string) (user.User, error) {
	return r.findOne(ctx, bson.D{{Key: "email", Value: email}})
}

func (r *Repository) FindByIDs(ctx context.Context, ids []user.ID) ([]user.User, error) {
	values := make([]string, 0, len(ids))
	for _, id := range ids {
		values = append(values, string(id))
	}

	cursor, err := r.users.Find(ctx, bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: values}}}})
	if err != nil {
		return nil, fmt.Errorf("find users: %w", err)
	}
	var docs []document
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("decode users: %w", err)
	}

	users := make([]user.User, 0, len(docs))
	for _, doc := range docs {
		users = append(users, doc.toUser())
	}
	return users, nil
}

func (r *Repository) findOne(ctx context.Context, filter bson.D) (user.User, error) {
	var doc document
	err := r.users.FindOne(ctx, filter).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return user.User{}, user.ErrNotFound
	}
	if err != nil {
		return user.User{}, fmt.Errorf("find user: %w", err)
	}
	return doc.toUser(), nil
}

func toDocument(u user.User) document {
	return document{
		ID:           string(u.ID),
		Email:        u.Email,
		Name:         u.Name,
		PasswordHash: u.PasswordHash,
		CreatedAt:    u.CreatedAt,
		UpdatedAt:    u.UpdatedAt,
	}
}

func (d document) toUser() user.User {
	return user.User{
		ID:           user.ID(d.ID),
		Email:        d.Email,
		Name:         d.Name,
		PasswordHash: d.PasswordHash,
		CreatedAt:    d.CreatedAt.UTC(),
		UpdatedAt:    d.UpdatedAt.UTC(),
	}
}
