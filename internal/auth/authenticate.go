package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"rinotravel-api/internal/user"
)

type Authenticate struct {
	sessions SessionRepository
}

func NewAuthenticate(sessions SessionRepository) *Authenticate {
	return &Authenticate{sessions: sessions}
}

func (a *Authenticate) Execute(ctx context.Context, token string) (user.ID, error) {
	s, err := a.sessions.FindByTokenHash(ctx, hashToken(token))
	if errors.Is(err, ErrSessionNotFound) {
		return "", errInvalidToken()
	}
	if err != nil {
		return "", fmt.Errorf("find session: %w", err)
	}
	if !time.Now().Before(s.ExpiresAt) {
		return "", errInvalidToken()
	}
	return s.UserID, nil
}
