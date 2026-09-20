//go:build integration

package mongorepo_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rinotravel-api/internal/auth"
	"rinotravel-api/internal/auth/mongorepo"
	"rinotravel-api/internal/platform/mongodb/mongotest"
)

func sampleSession(id, tokenHash string) auth.Session {
	now := time.Now().UTC().Truncate(time.Millisecond)
	return auth.Session{ID: id, UserID: "u-1", TokenHash: tokenHash, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
}

func TestSessionRepository_CreateFindDelete(t *testing.T) {
	ctx := context.Background()
	repo := mongorepo.NewSessionRepository(mongotest.Database(t))
	if err := repo.EnsureIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	want := sampleSession("s-1", "hash-1")

	if err := repo.Create(ctx, want); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	got, err := repo.FindByTokenHash(ctx, "hash-1")
	if err != nil || got != want {
		t.Errorf("FindByTokenHash() = %+v, %v; want %+v", got, err, want)
	}

	if err := repo.DeleteByTokenHash(ctx, "hash-1"); err != nil {
		t.Fatalf("DeleteByTokenHash() error = %v", err)
	}
	if _, err := repo.FindByTokenHash(ctx, "hash-1"); !errors.Is(err, auth.ErrSessionNotFound) {
		t.Errorf("FindByTokenHash() after delete error = %v, want ErrSessionNotFound", err)
	}
	if err := repo.DeleteByTokenHash(ctx, "hash-1"); err != nil {
		t.Errorf("deleting a missing session error = %v, want nil", err)
	}
}

func TestSessionRepository_TokenHashIsUnique(t *testing.T) {
	ctx := context.Background()
	repo := mongorepo.NewSessionRepository(mongotest.Database(t))
	if err := repo.EnsureIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, sampleSession("s-1", "same-hash")); err != nil {
		t.Fatal(err)
	}

	if err := repo.Create(ctx, sampleSession("s-2", "same-hash")); err == nil {
		t.Error("Create() with a duplicate token hash succeeded")
	}
}

func TestSessionRepository_HasTTLIndexOnExpiresAt(t *testing.T) {
	ctx := context.Background()
	db := mongotest.Database(t)
	repo := mongorepo.NewSessionRepository(db)
	if err := repo.EnsureIndexes(ctx); err != nil {
		t.Fatal(err)
	}

	cursor, err := db.Collection("sessions").Indexes().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var indexes []bson.M
	if err := cursor.All(ctx, &indexes); err != nil {
		t.Fatal(err)
	}

	for _, index := range indexes {
		if index["name"] == "expiresAt_1" {
			if _, ok := index["expireAfterSeconds"]; !ok {
				t.Error("expiresAt index is not a TTL index")
			}
			return
		}
	}
	t.Error("expiresAt index not found")
}
