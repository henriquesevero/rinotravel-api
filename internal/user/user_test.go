package user_test

import (
	"errors"
	"strings"
	"testing"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/user"
)

func fieldNames(t *testing.T, err error) []string {
	t.Helper()
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperror.KindValidation {
		t.Fatalf("error = %v, want a validation error", err)
	}
	names := make([]string, 0, len(appErr.Fields))
	for _, f := range appErr.Fields {
		names = append(names, f.Field)
	}
	return names
}

func TestRegistrationNormalize_CleansValidInput(t *testing.T) {
	got, err := user.Registration{
		Email:    "  Ana.Silva@Example.COM ",
		Name:     "  Ana Silva ",
		Password: "correct horse battery",
	}.Normalize()
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}

	if got.Email != "ana.silva@example.com" || got.Name != "Ana Silva" || got.Password != "correct horse battery" {
		t.Errorf("Normalize() = %+v", got)
	}
}

func TestRegistrationNormalize_RejectsInvalidEmail(t *testing.T) {
	for _, email := range []string{
		"",
		"plain",
		"missing-domain@",
		"@no-local.com",
		"no-dot@localhost",
		"Ana <ana@example.com>",
		"two@@example.com",
		strings.Repeat("a", 250) + "@example.com",
	} {
		t.Run(email, func(t *testing.T) {
			_, err := user.Registration{Email: email, Name: "Ana", Password: "correct horse battery"}.Normalize()
			if got := fieldNames(t, err); len(got) != 1 || got[0] != "email" {
				t.Errorf("invalid fields = %v, want [email]", got)
			}
		})
	}
}

func TestRegistrationNormalize_RejectsInvalidName(t *testing.T) {
	for name, value := range map[string]string{
		"empty":      "",
		"whitespace": "   ",
		"too long":   strings.Repeat("a", 101),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := user.Registration{Email: "ana@example.com", Name: value, Password: "correct horse battery"}.Normalize()
			if got := fieldNames(t, err); len(got) != 1 || got[0] != "name" {
				t.Errorf("invalid fields = %v, want [name]", got)
			}
		})
	}
}

func TestRegistrationNormalize_EnforcesPasswordLength(t *testing.T) {
	tests := []struct {
		name     string
		password string
		wantErr  bool
	}{
		{"too short", strings.Repeat("a", user.MinPasswordLength-1), true},
		{"minimum", strings.Repeat("a", user.MinPasswordLength), false},
		{"maximum", strings.Repeat("a", user.MaxPasswordLength), false},
		{"too long", strings.Repeat("a", user.MaxPasswordLength+1), true},
		{"counts characters not bytes", strings.Repeat("ç", user.MinPasswordLength), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := user.Registration{Email: "ana@example.com", Name: "Ana", Password: tt.password}.Normalize()
			if (err != nil) != tt.wantErr {
				t.Errorf("Normalize() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRegistrationNormalize_ReportsEveryInvalidField(t *testing.T) {
	_, err := user.Registration{}.Normalize()

	got := strings.Join(fieldNames(t, err), ",")
	if got != "email,name,password" {
		t.Errorf("invalid fields = %s, want email,name,password", got)
	}
}
