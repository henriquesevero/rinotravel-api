package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"rinotravel-api/internal/apperror"
)

const maxBodyBytes = 1 << 20

func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return apperror.BadRequest("body_too_large", "The request body is too large.")
		}
		return errMalformedJSON()
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errMalformedJSON()
	}
	return nil
}

func errMalformedJSON() *apperror.Error {
	return apperror.BadRequest("malformed_json", "The request body must be a single valid JSON object with the expected fields.")
}
