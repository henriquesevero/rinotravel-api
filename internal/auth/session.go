package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/platform/clock"
	"rinotravel-api/internal/platform/ids"
	"rinotravel-api/internal/user"
)

const (
	SessionTTL  = 30 * 24 * time.Hour
	tokenPrefix = "rt_"
)

var ErrSessionNotFound = errors.New("session not found")

type Session struct {
	ID        string
	UserID    user.ID
	TokenHash string
	CreatedAt time.Time
	ExpiresAt time.Time
}

type SessionRepository interface {
	Create(ctx context.Context, s Session) error
	FindByTokenHash(ctx context.Context, tokenHash string) (Session, error)
	DeleteByTokenHash(ctx context.Context, tokenHash string) error
}

type Result struct {
	Token     string
	ExpiresAt time.Time
	User      user.User
}

func issueSession(ctx context.Context, sessions SessionRepository, u user.User) (Result, error) {
	now := clock.Now()
	token, tokenHash := newToken()
	s := Session{
		ID:        ids.New(),
		UserID:    u.ID,
		TokenHash: tokenHash,
		CreatedAt: now,
		ExpiresAt: now.Add(SessionTTL),
	}
	if err := sessions.Create(ctx, s); err != nil {
		return Result{}, fmt.Errorf("create session: %w", err)
	}
	return Result{Token: token, ExpiresAt: s.ExpiresAt, User: u}, nil
}

func newToken() (token, tokenHash string) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	token = tokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	return token, hashToken(token)
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func errInvalidCredentials() *apperror.Error {
	return apperror.Unauthorized("invalid_credentials", "Invalid email or password.")
}

func errInvalidToken() *apperror.Error {
	return apperror.Unauthorized("invalid_token", "The session is invalid or has expired.")
}
