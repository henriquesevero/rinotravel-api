package kernel

import (
	"net/url"
	"strings"
	"unicode/utf8"

	"rinotravel-api/internal/apperror"
)

// Validator collects every field problem so one response can report them all.
type Validator struct {
	fields []apperror.FieldError
}

func (v *Validator) Add(field, message string) {
	v.fields = append(v.fields, apperror.FieldError{Field: field, Message: message})
}

func (v *Validator) Check(ok bool, field, message string) {
	if !ok {
		v.Add(field, message)
	}
}

// Text trims value and enforces presence and a maximum length in characters.
func (v *Validator) Text(field, value string, required bool, max int) string {
	value = strings.TrimSpace(value)
	switch {
	case required && value == "":
		v.Add(field, "is required")
	case utf8.RuneCountInString(value) > max:
		v.Add(field, "must have at most "+itoa(max)+" characters")
	}
	return value
}

func (v *Validator) HTTPURL(field, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		v.Add(field, "must be an http or https URL")
	}
	return value
}

func (v *Validator) Err() error {
	if len(v.fields) == 0 {
		return nil
	}
	return apperror.Validation(v.fields...)
}

func (v *Validator) HasErrors() bool {
	return len(v.fields) > 0
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for ; n > 0; n /= 10 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
	}
	return string(digits)
}
