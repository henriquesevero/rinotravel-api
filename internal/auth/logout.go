package auth

import (
	"context"
	"fmt"
)

type Logout struct {
	sessions SessionRepository
}

func NewLogout(sessions SessionRepository) *Logout {
	return &Logout{sessions: sessions}
}

func (l *Logout) Execute(ctx context.Context, token string) error {
	if err := l.sessions.DeleteByTokenHash(ctx, hashToken(token)); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}
