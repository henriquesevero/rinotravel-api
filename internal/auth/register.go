package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/platform/clock"
	"rinotravel-api/internal/platform/ids"
	"rinotravel-api/internal/user"
)

type Users interface {
	Create(ctx context.Context, u user.User) error
	FindByEmail(ctx context.Context, email string) (user.User, error)
}

type PasswordHasher interface {
	Hash(password string) (string, error)
	Verify(password, encodedHash string) (bool, error)
}

type Register struct {
	users      Users
	sessions   SessionRepository
	hasher     PasswordHasher
	codeDigest [sha256.Size]byte
}

type RegisterInput struct {
	Email            string
	Name             string
	Password         string
	RegistrationCode string
}

func NewRegister(users Users, sessions SessionRepository, hasher PasswordHasher, registrationCode string) *Register {
	return &Register{
		users:      users,
		sessions:   sessions,
		hasher:     hasher,
		codeDigest: sha256.Sum256([]byte(registrationCode)),
	}
}

func (r *Register) Execute(ctx context.Context, in RegisterInput) (Result, error) {
	given := sha256.Sum256([]byte(in.RegistrationCode))
	if subtle.ConstantTimeCompare(given[:], r.codeDigest[:]) != 1 {
		return Result{}, apperror.Forbidden("invalid_registration_code", "The registration code is invalid.")
	}

	reg, err := user.Registration{Email: in.Email, Name: in.Name, Password: in.Password}.Normalize()
	if err != nil {
		return Result{}, err
	}

	passwordHash, err := r.hasher.Hash(reg.Password)
	if err != nil {
		return Result{}, fmt.Errorf("hash password: %w", err)
	}

	now := clock.Now()
	u := user.User{
		ID:           user.ID(ids.New()),
		Email:        reg.Email,
		Name:         reg.Name,
		PasswordHash: passwordHash,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := r.users.Create(ctx, u); err != nil {
		if errors.Is(err, user.ErrEmailTaken) {
			return Result{}, apperror.Conflict("email_taken", "An account with this email already exists.")
		}
		return Result{}, fmt.Errorf("create user: %w", err)
	}

	return issueSession(ctx, r.sessions, u)
}
