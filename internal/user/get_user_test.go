package user_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/auth/authtest"
	"rinotravel-api/internal/user"
)

type failingReader struct{ err error }

func (f failingReader) FindByID(context.Context, user.ID) (user.User, error) {
	return user.User{}, f.err
}

func TestGetUser(t *testing.T) {
	ctx := context.Background()
	users := authtest.NewUsers()
	want := user.User{ID: "u-1", Email: "ana@example.com", Name: "Ana", CreatedAt: time.Now().UTC()}
	if err := users.Create(ctx, want); err != nil {
		t.Fatal(err)
	}

	t.Run("returns the user", func(t *testing.T) {
		got, err := user.NewGetUser(users).Execute(ctx, "u-1")
		if err != nil || got != want {
			t.Errorf("Execute() = %+v, %v; want %+v", got, err, want)
		}
	})

	t.Run("unknown user is not found", func(t *testing.T) {
		_, err := user.NewGetUser(users).Execute(ctx, "missing")

		var appErr *apperror.Error
		if !errors.As(err, &appErr) || appErr.Kind != apperror.KindNotFound || appErr.Code != "user_not_found" {
			t.Errorf("Execute() error = %v, want user_not_found", err)
		}
	})

	t.Run("infrastructure failure is not an app error", func(t *testing.T) {
		boom := errors.New("connection reset")
		_, err := user.NewGetUser(failingReader{err: boom}).Execute(ctx, "u-1")

		var appErr *apperror.Error
		if errors.As(err, &appErr) || !errors.Is(err, boom) {
			t.Errorf("Execute() error = %v, want the wrapped infrastructure error", err)
		}
	})
}
