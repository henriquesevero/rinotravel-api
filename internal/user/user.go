package user

import (
	"errors"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"rinotravel-api/internal/apperror"
)

const (
	MinPasswordLength = 10
	MaxPasswordLength = 128
	maxNameLength     = 100
	maxEmailLength    = 254
)

var (
	ErrNotFound   = errors.New("user not found")
	ErrEmailTaken = errors.New("email already registered")
)

type ID string

type User struct {
	ID           ID
	Email        string
	Name         string
	PasswordHash string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Registration struct {
	Email    string
	Name     string
	Password string
}

func (r Registration) Normalize() (Registration, error) {
	var fields []apperror.FieldError

	email := strings.ToLower(strings.TrimSpace(r.Email))
	if !validEmail(email) {
		fields = append(fields, apperror.FieldError{Field: "email", Message: "must be a valid email address"})
	}

	name := strings.TrimSpace(r.Name)
	if name == "" || utf8.RuneCountInString(name) > maxNameLength {
		fields = append(fields, apperror.FieldError{Field: "name", Message: "is required and must have at most 100 characters"})
	}

	if n := utf8.RuneCountInString(r.Password); n < MinPasswordLength || n > MaxPasswordLength {
		fields = append(fields, apperror.FieldError{Field: "password", Message: "must have between 10 and 128 characters"})
	}

	if len(fields) > 0 {
		return Registration{}, apperror.Validation(fields...)
	}
	return Registration{Email: email, Name: name, Password: r.Password}, nil
}

func validEmail(email string) bool {
	if email == "" || len(email) > maxEmailLength {
		return false
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return false
	}
	return strings.Contains(email[strings.LastIndex(email, "@"):], ".")
}
