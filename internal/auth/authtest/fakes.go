package authtest

import (
	"context"
	"strings"
	"sync"
	"time"

	"rinotravel-api/internal/auth"
	"rinotravel-api/internal/user"
)

type Users struct {
	mu   sync.Mutex
	byID map[user.ID]user.User
}

func NewUsers() *Users {
	return &Users{byID: make(map[user.ID]user.User)}
}

func (s *Users) Create(_ context.Context, u user.User) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, existing := range s.byID {
		if existing.Email == u.Email {
			return user.ErrEmailTaken
		}
	}
	s.byID[u.ID] = u
	return nil
}

func (s *Users) FindByID(_ context.Context, id user.ID) (user.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.byID[id]
	if !ok {
		return user.User{}, user.ErrNotFound
	}
	return u, nil
}

func (s *Users) FindByEmail(_ context.Context, email string) (user.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, u := range s.byID {
		if u.Email == email {
			return u, nil
		}
	}
	return user.User{}, user.ErrNotFound
}

func (s *Users) FindByIDs(_ context.Context, ids []user.ID) ([]user.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var found []user.User
	for _, id := range ids {
		if u, ok := s.byID[id]; ok {
			found = append(found, u)
		}
	}
	return found, nil
}

func (s *Users) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.byID)
}

type Sessions struct {
	mu     sync.Mutex
	byHash map[string]auth.Session
}

func NewSessions() *Sessions {
	return &Sessions{byHash: make(map[string]auth.Session)}
}

func (s *Sessions) Create(_ context.Context, session auth.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byHash[session.TokenHash] = session
	return nil
}

func (s *Sessions) FindByTokenHash(_ context.Context, tokenHash string) (auth.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.byHash[tokenHash]
	if !ok {
		return auth.Session{}, auth.ErrSessionNotFound
	}
	return session, nil
}

func (s *Sessions) DeleteByTokenHash(_ context.Context, tokenHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byHash, tokenHash)
	return nil
}

func (s *Sessions) All() []auth.Session {
	s.mu.Lock()
	defer s.mu.Unlock()

	sessions := make([]auth.Session, 0, len(s.byHash))
	for _, session := range s.byHash {
		sessions = append(sessions, session)
	}
	return sessions
}

func (s *Sessions) ExpireAll() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for hash, session := range s.byHash {
		session.ExpiresAt = time.Now().Add(-time.Minute)
		s.byHash[hash] = session
	}
}

type Hasher struct {
	mu          sync.Mutex
	verifyCalls int
}

const hashPrefix = "hashed:"

func (h *Hasher) Hash(password string) (string, error) {
	return hashPrefix + password, nil
}

func (h *Hasher) Verify(password, encodedHash string) (bool, error) {
	h.mu.Lock()
	h.verifyCalls++
	h.mu.Unlock()
	return strings.TrimPrefix(encodedHash, hashPrefix) == password, nil
}

func (h *Hasher) VerifyCalls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.verifyCalls
}
