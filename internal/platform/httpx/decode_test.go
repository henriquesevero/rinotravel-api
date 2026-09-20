package httpx_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/platform/httpx"
)

type payload struct {
	Name string `json:"name"`
}

func decode(body string) (payload, error) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	var dst payload
	err := httpx.DecodeJSON(httptest.NewRecorder(), req, &dst)
	return dst, err
}

func TestDecodeJSON_AcceptsValidBody(t *testing.T) {
	got, err := decode(`{"name":"Ana"}`)
	if err != nil || got.Name != "Ana" {
		t.Errorf("DecodeJSON() = %+v, %v", got, err)
	}
}

func TestDecodeJSON_RejectsBadBodies(t *testing.T) {
	tests := map[string]struct {
		body     string
		wantCode string
	}{
		"empty body":       {"", "malformed_json"},
		"not json":         {"name=Ana", "malformed_json"},
		"wrong type":       {`{"name":42}`, "malformed_json"},
		"unknown field":    {`{"name":"Ana","admin":true}`, "malformed_json"},
		"trailing data":    {`{"name":"Ana"}{"name":"Bia"}`, "malformed_json"},
		"trailing garbage": {`{"name":"Ana"} nope`, "malformed_json"},
		"too large":        {`{"name":"` + strings.Repeat("a", 2<<20) + `"}`, "body_too_large"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := decode(tt.body)

			var appErr *apperror.Error
			if !errors.As(err, &appErr) || appErr.Kind != apperror.KindBadRequest || appErr.Code != tt.wantCode {
				t.Errorf("DecodeJSON() error = %v, want bad request %s", err, tt.wantCode)
			}
		})
	}
}
