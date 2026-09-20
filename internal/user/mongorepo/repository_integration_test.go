//go:build integration

package mongorepo_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"rinotravel-api/internal/platform/mongodb/mongotest"
	"rinotravel-api/internal/user"
	"rinotravel-api/internal/user/mongorepo"
)

func newRepository(t *testing.T) *mongorepo.Repository {
	t.Helper()
	repo := mongorepo.New(mongotest.Database(t))
	if err := repo.EnsureIndexes(context.Background()); err != nil {
		t.Fatalf("EnsureIndexes() error = %v", err)
	}
	return repo
}

func sampleUser(id, email string) user.User {
	now := time.Now().UTC().Truncate(time.Millisecond)
	return user.User{ID: user.ID(id), Email: email, Name: "Ana", PasswordHash: "$argon2id$hash", CreatedAt: now, UpdatedAt: now}
}

func TestRepository_CreateAndFind(t *testing.T) {
	ctx := context.Background()
	repo := newRepository(t)
	want := sampleUser("u-1", "ana@example.com")

	if err := repo.Create(ctx, want); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	byID, err := repo.FindByID(ctx, "u-1")
	if err != nil || byID != want {
		t.Errorf("FindByID() = %+v, %v; want %+v", byID, err, want)
	}
	byEmail, err := repo.FindByEmail(ctx, "ana@example.com")
	if err != nil || byEmail != want {
		t.Errorf("FindByEmail() = %+v, %v; want %+v", byEmail, err, want)
	}
}

func TestRepository_NotFound(t *testing.T) {
	ctx := context.Background()
	repo := newRepository(t)

	if _, err := repo.FindByID(ctx, "missing"); !errors.Is(err, user.ErrNotFound) {
		t.Errorf("FindByID() error = %v, want ErrNotFound", err)
	}
	if _, err := repo.FindByEmail(ctx, "missing@example.com"); !errors.Is(err, user.ErrNotFound) {
		t.Errorf("FindByEmail() error = %v, want ErrNotFound", err)
	}
}

func TestRepository_EmailIsUnique(t *testing.T) {
	ctx := context.Background()
	repo := newRepository(t)
	if err := repo.Create(ctx, sampleUser("u-1", "ana@example.com")); err != nil {
		t.Fatal(err)
	}

	err := repo.Create(ctx, sampleUser("u-2", "ana@example.com"))

	if !errors.Is(err, user.ErrEmailTaken) {
		t.Errorf("Create() error = %v, want ErrEmailTaken", err)
	}
}

func TestRepository_EnsureIndexesIsIdempotent(t *testing.T) {
	repo := newRepository(t)

	if err := repo.EnsureIndexes(context.Background()); err != nil {
		t.Errorf("second EnsureIndexes() error = %v", err)
	}
}

func TestRepository_NoOperatorInjectionThroughEmail(t *testing.T) {
	ctx := context.Background()
	repo := newRepository(t)
	if err := repo.Create(ctx, sampleUser("u-1", "ana@example.com")); err != nil {
		t.Fatal(err)
	}

	if _, err := repo.FindByEmail(ctx, `{"$ne": ""}`); !errors.Is(err, user.ErrNotFound) {
		t.Errorf("FindByEmail() error = %v, want ErrNotFound", err)
	}
}

func TestRepository_FindByIDs(t *testing.T) {
	ctx := context.Background()
	repo := newRepository(t)
	for _, u := range []user.User{sampleUser("u-1", "a@example.com"), sampleUser("u-2", "b@example.com"), sampleUser("u-3", "c@example.com")} {
		if err := repo.Create(ctx, u); err != nil {
			t.Fatal(err)
		}
	}

	found, err := repo.FindByIDs(ctx, []user.ID{"u-1", "u-3", "missing"})
	if err != nil {
		t.Fatalf("FindByIDs() error = %v", err)
	}

	got := map[user.ID]string{}
	for _, u := range found {
		got[u.ID] = u.Email
	}
	if len(got) != 2 || got["u-1"] != "a@example.com" || got["u-3"] != "c@example.com" {
		t.Errorf("FindByIDs() = %v, want only u-1 and u-3", got)
	}

	if none, err := repo.FindByIDs(ctx, nil); err != nil || len(none) != 0 {
		t.Errorf("FindByIDs(nil) = %v, %v; want empty", none, err)
	}
}
