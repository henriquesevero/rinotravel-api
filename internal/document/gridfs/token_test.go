package gridfs

import (
	"strings"
	"testing"
	"time"
)

var secret = []byte(strings.Repeat("s", 32))

func TestTokenRoundTrip(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	in := claims{Op: opPut, Key: "development/trips/t/docs/d", Expires: now.Add(time.Minute).Unix(), Size: 10, SHA256: "abc", ContentType: "application/pdf"}

	got, err := verify(secret, sign(secret, in), now)

	if err != nil || got != in {
		t.Errorf("verify() = %+v, %v; want %+v", got, err, in)
	}
}

func TestTokenRejectsTamperingWrongSecretAndExpiry(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	token := sign(secret, claims{Op: opGet, Key: "k", Expires: now.Add(time.Minute).Unix()})
	body, sig, _ := strings.Cut(token, ".")

	cases := map[string]string{
		"no signature":       body,
		"empty":              "",
		"garbage":            "not.a.token",
		"other secret":       sign([]byte(strings.Repeat("x", 32)), claims{Op: opGet, Key: "k", Expires: now.Add(time.Minute).Unix()}),
		"tampered payload":   "eyJvIjoiZ2V0Iiwiayi6Im90aGVyIiwiZSI6OTk5OTk5OTk5OX0." + sig,
		"tampered signature": body + "." + strings.Repeat("A", len(sig)),
	}
	for name, bad := range cases {
		if _, err := verify(secret, bad, now); err != errInvalidToken {
			t.Errorf("%s: error = %v, want errInvalidToken", name, err)
		}
	}
	if _, err := verify(secret, token, now.Add(2*time.Minute)); err != errExpiredToken {
		t.Errorf("expired token error = %v, want errExpiredToken", err)
	}
	if _, err := verify(secret, token, now.Add(time.Minute)); err != errExpiredToken {
		t.Errorf("a token is invalid exactly at its deadline, got %v", err)
	}
}
