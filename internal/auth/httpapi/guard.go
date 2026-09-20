package httpapi

import (
	"context"
	"net/http"
	"strings"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/auth"
	"rinotravel-api/internal/platform/httpx"
	"rinotravel-api/internal/user"
)

type userIDKey struct{}

type Guard struct {
	authenticate *auth.Authenticate
}

func NewGuard(authenticate *auth.Authenticate) Guard {
	return Guard{authenticate: authenticate}
}

func (g Guard) Require(next httpx.HandlerFunc) httpx.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		token, ok := bearerToken(r)
		if !ok {
			w.Header().Set("WWW-Authenticate", "Bearer")
			return apperror.Unauthorized("unauthenticated", "Authentication is required.")
		}

		userID, err := g.authenticate.Execute(r.Context(), token)
		if err != nil {
			w.Header().Set("WWW-Authenticate", "Bearer")
			return err
		}
		return next(w, r.WithContext(context.WithValue(r.Context(), userIDKey{}, userID)))
	}
}

func UserID(ctx context.Context) user.ID {
	id, _ := ctx.Value(userIDKey{}).(user.ID)
	return id
}

func bearerToken(r *http.Request) (string, bool) {
	scheme, token, found := strings.Cut(r.Header.Get("Authorization"), " ")
	token = strings.TrimSpace(token)
	if !found || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return "", false
	}
	return token, true
}
