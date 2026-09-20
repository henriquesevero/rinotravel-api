package auth_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/auth"
	"rinotravel-api/internal/auth/authtest"
	"rinotravel-api/internal/user"
)

const registrationCode = "dev-registration-code"

type fixture struct {
	users    *authtest.Users
	sessions *authtest.Sessions
	hasher   *authtest.Hasher
	register *auth.Register
	login    *auth.Login
	logout   *auth.Logout
	authn    *auth.Authenticate
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	f := fixture{
		users:    authtest.NewUsers(),
		sessions: authtest.NewSessions(),
		hasher:   &authtest.Hasher{},
	}
	login, err := auth.NewLogin(f.users, f.sessions, f.hasher)
	if err != nil {
		t.Fatalf("NewLogin() error = %v", err)
	}
	f.register = auth.NewRegister(f.users, f.sessions, f.hasher, registrationCode)
	f.login = login
	f.logout = auth.NewLogout(f.sessions)
	f.authn = auth.NewAuthenticate(f.sessions)
	return f
}

func validRegistration() auth.RegisterInput {
	return auth.RegisterInput{
		Email:            "Ana@Example.com",
		Name:             "Ana",
		Password:         "correct horse battery",
		RegistrationCode: registrationCode,
	}
}

func requireAppError(t *testing.T, err error, kind apperror.Kind, code string) {
	t.Helper()
	var appErr *apperror.Error
	if !errors.As(err, &appErr) {
		t.Fatalf("error = %v, want an *apperror.Error", err)
	}
	if appErr.Kind != kind || appErr.Code != code {
		t.Errorf("error = {kind %d, code %q}, want {kind %d, code %q}", appErr.Kind, appErr.Code, kind, code)
	}
}

func TestRegister_CreatesUserAndSession(t *testing.T) {
	f := newFixture(t)
	before := time.Now().Truncate(time.Millisecond)

	result, err := f.register.Execute(context.Background(), validRegistration())
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if result.User.Email != "ana@example.com" || result.User.ID == "" {
		t.Errorf("unexpected user: %+v", result.User)
	}
	if result.User.PasswordHash != "hashed:correct horse battery" {
		t.Errorf("PasswordHash = %q, want the hasher output", result.User.PasswordHash)
	}
	if !strings.HasPrefix(result.Token, "rt_") {
		t.Errorf("Token = %q, want rt_ prefix", result.Token)
	}
	if min := before.Add(auth.SessionTTL); result.ExpiresAt.Before(min) || result.ExpiresAt.After(min.Add(time.Minute)) {
		t.Errorf("ExpiresAt = %v, want about %v", result.ExpiresAt, min)
	}

	sessions := f.sessions.All()
	if len(sessions) != 1 {
		t.Fatalf("stored sessions = %d, want 1", len(sessions))
	}
	if sessions[0].TokenHash == result.Token || strings.Contains(sessions[0].TokenHash, result.Token) {
		t.Error("the raw token was stored instead of its hash")
	}
	if sessions[0].UserID != result.User.ID {
		t.Errorf("session user = %q, want %q", sessions[0].UserID, result.User.ID)
	}
}

func TestRegister_RejectsWrongRegistrationCode(t *testing.T) {
	for name, code := range map[string]string{
		"wrong":   "not-the-code",
		"empty":   "",
		"prefix":  registrationCode[:5],
		"suffix+": registrationCode + "x",
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			in := validRegistration()
			in.RegistrationCode = code

			_, err := f.register.Execute(context.Background(), in)

			requireAppError(t, err, apperror.KindForbidden, "invalid_registration_code")
			if f.users.Len() != 0 || len(f.sessions.All()) != 0 {
				t.Error("registration with a wrong code created data")
			}
		})
	}
}

func TestRegister_ChecksCodeBeforeValidatingInput(t *testing.T) {
	f := newFixture(t)

	_, err := f.register.Execute(context.Background(), auth.RegisterInput{RegistrationCode: "wrong"})

	requireAppError(t, err, apperror.KindForbidden, "invalid_registration_code")
}

func TestRegister_ValidatesInput(t *testing.T) {
	f := newFixture(t)
	in := validRegistration()
	in.Password = "short"

	_, err := f.register.Execute(context.Background(), in)

	requireAppError(t, err, apperror.KindValidation, "validation_failed")
	if f.users.Len() != 0 {
		t.Error("invalid registration created a user")
	}
}

func TestRegister_RejectsDuplicateEmailIgnoringCase(t *testing.T) {
	f := newFixture(t)
	if _, err := f.register.Execute(context.Background(), validRegistration()); err != nil {
		t.Fatal(err)
	}

	in := validRegistration()
	in.Email = "  ANA@example.COM"
	_, err := f.register.Execute(context.Background(), in)

	requireAppError(t, err, apperror.KindConflict, "email_taken")
	if f.users.Len() != 1 {
		t.Errorf("users = %d, want 1", f.users.Len())
	}
}

func TestLogin_IssuesSessionForValidCredentials(t *testing.T) {
	f := newFixture(t)
	registered, err := f.register.Execute(context.Background(), validRegistration())
	if err != nil {
		t.Fatal(err)
	}

	result, err := f.login.Execute(context.Background(), auth.LoginInput{Email: "  ANA@example.com ", Password: "correct horse battery"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if result.User.ID != registered.User.ID {
		t.Errorf("logged in as %q, want %q", result.User.ID, registered.User.ID)
	}
	if result.Token == registered.Token {
		t.Error("login reused the registration token")
	}
	if len(f.sessions.All()) != 2 {
		t.Errorf("sessions = %d, want one per login", len(f.sessions.All()))
	}
}

func TestLogin_WrongPasswordAndUnknownEmailAreIndistinguishable(t *testing.T) {
	f := newFixture(t)
	if _, err := f.register.Execute(context.Background(), validRegistration()); err != nil {
		t.Fatal(err)
	}

	_, wrongPassword := f.login.Execute(context.Background(), auth.LoginInput{Email: "ana@example.com", Password: "wrong password!"})
	_, unknownEmail := f.login.Execute(context.Background(), auth.LoginInput{Email: "nobody@example.com", Password: "correct horse battery"})

	requireAppError(t, wrongPassword, apperror.KindUnauthorized, "invalid_credentials")
	requireAppError(t, unknownEmail, apperror.KindUnauthorized, "invalid_credentials")
	if wrongPassword.Error() != unknownEmail.Error() {
		t.Errorf("errors differ: %q vs %q", wrongPassword, unknownEmail)
	}
}

func TestLogin_HashesEvenWhenEmailIsUnknown(t *testing.T) {
	f := newFixture(t)

	_, _ = f.login.Execute(context.Background(), auth.LoginInput{Email: "nobody@example.com", Password: "whatever password"})

	if f.hasher.VerifyCalls() != 1 {
		t.Errorf("Verify calls = %d, want 1 (equalizing work for unknown emails)", f.hasher.VerifyCalls())
	}
}

func TestLogin_RejectsOversizedPasswordWithoutHashing(t *testing.T) {
	f := newFixture(t)

	_, err := f.login.Execute(context.Background(), auth.LoginInput{
		Email:    "ana@example.com",
		Password: strings.Repeat("a", user.MaxPasswordLength+1),
	})

	requireAppError(t, err, apperror.KindUnauthorized, "invalid_credentials")
	if f.hasher.VerifyCalls() != 0 {
		t.Errorf("Verify calls = %d, want 0", f.hasher.VerifyCalls())
	}
}

func TestAuthenticate(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	registered, err := f.register.Execute(ctx, validRegistration())
	if err != nil {
		t.Fatal(err)
	}

	t.Run("valid token resolves the user", func(t *testing.T) {
		id, err := f.authn.Execute(ctx, registered.Token)
		if err != nil || id != registered.User.ID {
			t.Errorf("Execute() = %q, %v; want %q", id, err, registered.User.ID)
		}
	})

	t.Run("unknown token is rejected", func(t *testing.T) {
		_, err := f.authn.Execute(ctx, "rt_unknown")
		requireAppError(t, err, apperror.KindUnauthorized, "invalid_token")
	})

	t.Run("the stored hash is not usable as a token", func(t *testing.T) {
		_, err := f.authn.Execute(ctx, f.sessions.All()[0].TokenHash)
		requireAppError(t, err, apperror.KindUnauthorized, "invalid_token")
	})

	t.Run("expired session is rejected", func(t *testing.T) {
		f.sessions.ExpireAll()

		_, err := f.authn.Execute(ctx, registered.Token)
		requireAppError(t, err, apperror.KindUnauthorized, "invalid_token")
	})
}

func TestLogout_RevokesOnlyThatSession(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	first, err := f.register.Execute(ctx, validRegistration())
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.login.Execute(ctx, auth.LoginInput{Email: "ana@example.com", Password: "correct horse battery"})
	if err != nil {
		t.Fatal(err)
	}

	if err := f.logout.Execute(ctx, first.Token); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}

	if _, err := f.authn.Execute(ctx, first.Token); err == nil {
		t.Error("logged out token is still valid")
	}
	if _, err := f.authn.Execute(ctx, second.Token); err != nil {
		t.Errorf("other session was revoked: %v", err)
	}
}

func TestLogout_IsIdempotent(t *testing.T) {
	f := newFixture(t)

	if err := f.logout.Execute(context.Background(), "rt_never-existed"); err != nil {
		t.Errorf("Logout() error = %v, want nil", err)
	}
}
