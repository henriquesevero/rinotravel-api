package mongorepo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"rinotravel-api/internal/auth"
	"rinotravel-api/internal/user"
)

const collectionName = "sessions"

type SessionRepository struct {
	sessions *mongo.Collection
}

func NewSessionRepository(db *mongo.Database) *SessionRepository {
	return &SessionRepository{sessions: db.Collection(collectionName)}
}

type document struct {
	ID        string    `bson:"_id"`
	UserID    string    `bson:"userId"`
	TokenHash string    `bson:"tokenHash"`
	CreatedAt time.Time `bson:"createdAt"`
	ExpiresAt time.Time `bson:"expiresAt"`
}

func (r *SessionRepository) EnsureIndexes(ctx context.Context) error {
	_, err := r.sessions.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "tokenHash", Value: 1}},
			Options: options.Index().SetUnique(true),
		},
		{
			Keys:    bson.D{{Key: "expiresAt", Value: 1}},
			Options: options.Index().SetExpireAfterSeconds(0),
		},
	})
	if err != nil {
		return fmt.Errorf("create sessions indexes: %w", err)
	}
	return nil
}

func (r *SessionRepository) Create(ctx context.Context, s auth.Session) error {
	_, err := r.sessions.InsertOne(ctx, document{
		ID:        s.ID,
		UserID:    string(s.UserID),
		TokenHash: s.TokenHash,
		CreatedAt: s.CreatedAt,
		ExpiresAt: s.ExpiresAt,
	})
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

func (r *SessionRepository) FindByTokenHash(ctx context.Context, tokenHash string) (auth.Session, error) {
	var doc document
	err := r.sessions.FindOne(ctx, bson.D{{Key: "tokenHash", Value: tokenHash}}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return auth.Session{}, auth.ErrSessionNotFound
	}
	if err != nil {
		return auth.Session{}, fmt.Errorf("find session: %w", err)
	}
	return auth.Session{
		ID:        doc.ID,
		UserID:    user.ID(doc.UserID),
		TokenHash: doc.TokenHash,
		CreatedAt: doc.CreatedAt.UTC(),
		ExpiresAt: doc.ExpiresAt.UTC(),
	}, nil
}

func (r *SessionRepository) DeleteByTokenHash(ctx context.Context, tokenHash string) error {
	if _, err := r.sessions.DeleteOne(ctx, bson.D{{Key: "tokenHash", Value: tokenHash}}); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}
