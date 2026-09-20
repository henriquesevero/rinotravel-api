package gridfs

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var (
	errInvalidToken = errors.New("invalid link")
	errExpiredToken = errors.New("expired link")
)

const (
	opPut = "put"
	opGet = "get"
)

// claims are everything a signed link authorizes: one operation on one object, until a deadline.
// For uploads they also pin the exact size and checksum the client declared.
type claims struct {
	Op          string `json:"o"`
	Key         string `json:"k"`
	Expires     int64  `json:"e"`
	Size        int64  `json:"s,omitempty"`
	SHA256      string `json:"h,omitempty"`
	ContentType string `json:"c,omitempty"`
	FileName    string `json:"f,omitempty"`
}

func sign(secret []byte, c claims) string {
	payload, _ := json.Marshal(c)
	body := base64.RawURLEncoding.EncodeToString(payload)
	return body + "." + base64.RawURLEncoding.EncodeToString(mac(secret, body))
}

func verify(secret []byte, token string, now time.Time) (claims, error) {
	body, signature, ok := strings.Cut(token, ".")
	if !ok {
		return claims{}, errInvalidToken
	}
	given, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil || !hmac.Equal(given, mac(secret, body)) {
		return claims{}, errInvalidToken
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return claims{}, errInvalidToken
	}
	var c claims
	if err := json.Unmarshal(raw, &c); err != nil {
		return claims{}, errInvalidToken
	}
	if !now.Before(time.Unix(c.Expires, 0)) {
		return claims{}, errExpiredToken
	}
	return c, nil
}

func mac(secret []byte, body string) []byte {
	h := hmac.New(sha256.New, secret)
	h.Write([]byte(body))
	return h.Sum(nil)
}
