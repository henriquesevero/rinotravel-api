package kernel

import (
	"bytes"
	"encoding/json"

	"rinotravel-api/internal/apperror"
)

// Optional tells "field absent" (leave unchanged) from "null" (clear it) from a value (set it),
// which a plain pointer cannot in a PATCH body.
type Optional[T any] struct {
	Set   bool
	Clear bool
	Value T
}

func (o *Optional[T]) UnmarshalJSON(data []byte) error {
	o.Set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		o.Clear = true
		return nil
	}
	return json.Unmarshal(data, &o.Value)
}

// Versioned carries the base version a client saw. Patch types embed it so REST and sync share
// one optimistic-concurrency contract.
type Versioned struct {
	BaseVersion *int64 `json:"baseVersion"`
}

func (v Versioned) Version() *int64 { return v.BaseVersion }

func (v *Versioned) SetVersion(version int64) { v.BaseVersion = &version }

func (v Versioned) Require() (int64, error) {
	if v.BaseVersion == nil {
		return 0, apperror.Validation(apperror.FieldError{Field: "baseVersion", Message: "is required"})
	}
	return *v.BaseVersion, nil
}
