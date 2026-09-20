package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"rinotravel-api/internal/platform/ids"
	"rinotravel-api/internal/user"
)

type Login struct {
	users     Users
	sessions  SessionRepository
	hasher    PasswordHasher
	dummyHash string
}

type LoginInput struct {
	Email    string
	Password string
}

// The dummy hash lets Execute spend the same hashing time for unknown emails,
// so response time does not reveal which emails are registered.
func NewLogin(users Users, sessions SessionRepository, hasher PasswordHasher) (*Login, error) {
	dummyHash, err := hasher.Hash(ids.New())
	if err != nil {
		return nil, fmt.Errorf("hash dummy password: %w", err)
	}
	return &Login{users: users, sessions: sessions, hasher: hasher, dummyHash: dummyHash}, nil
}

func (l *Login) Execute(ctx context.Context, in LoginInput) (Result, error) {
	if utf8.RuneCountInString(in.Password) > user.MaxPasswordLength {
		return Result{}, errInvalidCredentials()
	}

	u, err := l.users.FindByEmail(ctx, strings.ToLower(strings.TrimSpace(in.Email)))
	if errors.Is(err, user.ErrNotFound) {
		_, _ = l.hasher.Verify(in.Password, l.dummyHash)
		return Result{}, errInvalidCredentials()
	}
	if err != nil {
		return Result{}, fmt.Errorf("find user: %w", err)
	}

	ok, err := l.hasher.Verify(in.Password, u.PasswordHash)
	if err != nil {
		return Result{}, fmt.Errorf("verify password: %w", err)
	}
	if !ok {
		return Result{}, errInvalidCredentials()
	}

	return issueSession(ctx, l.sessions, u)
}
