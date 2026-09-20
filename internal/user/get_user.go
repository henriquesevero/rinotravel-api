package user

import (
	"context"
	"errors"
	"fmt"

	"rinotravel-api/internal/apperror"
)

type Reader interface {
	FindByID(ctx context.Context, id ID) (User, error)
}

type GetUser struct {
	users Reader
}

func NewGetUser(users Reader) *GetUser {
	return &GetUser{users: users}
}

func (g *GetUser) Execute(ctx context.Context, id ID) (User, error) {
	u, err := g.users.FindByID(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return User{}, apperror.NotFound("user_not_found", "User not found.")
	}
	if err != nil {
		return User{}, fmt.Errorf("find user: %w", err)
	}
	return u, nil
}
