package syncengine

import (
	"encoding/base64"
	"encoding/json"
	"time"

	"rinotravel-api/internal/apperror"
)

type cursor struct {
	Seq      int64 `json:"s"`
	IssuedAt int64 `json:"t"`
}

// The cursor is opaque to clients so its content can change without a protocol change.
func encodeCursor(seq int64, issuedAt time.Time) string {
	raw, _ := json.Marshal(cursor{Seq: seq, IssuedAt: issuedAt.Unix()})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeCursor(token string) (cursor, error) {
	var c cursor
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err == nil {
		err = json.Unmarshal(raw, &c)
	}
	if err != nil || c.Seq < 0 || c.IssuedAt <= 0 {
		return cursor{}, apperror.BadRequest("invalid_cursor", "The cursor is not valid. Start a full sync without one.")
	}
	return c, nil
}
