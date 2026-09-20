package apperror

type Kind int

const (
	KindBadRequest Kind = iota + 1
	KindUnauthorized
	KindForbidden
	KindNotFound
	KindConflict
	KindValidation
	KindTooManyRequests
)

type FieldError struct {
	Field   string
	Message string
}

type Error struct {
	Kind    Kind
	Code    string
	Message string
	Fields  []FieldError
}

func (e *Error) Error() string {
	return e.Code + ": " + e.Message
}

func BadRequest(code, message string) *Error {
	return &Error{Kind: KindBadRequest, Code: code, Message: message}
}

func Unauthorized(code, message string) *Error {
	return &Error{Kind: KindUnauthorized, Code: code, Message: message}
}

func Forbidden(code, message string) *Error {
	return &Error{Kind: KindForbidden, Code: code, Message: message}
}

func NotFound(code, message string) *Error {
	return &Error{Kind: KindNotFound, Code: code, Message: message}
}

func Conflict(code, message string) *Error {
	return &Error{Kind: KindConflict, Code: code, Message: message}
}

func TooManyRequests(code, message string) *Error {
	return &Error{Kind: KindTooManyRequests, Code: code, Message: message}
}

func Unprocessable(code, message string) *Error {
	return &Error{Kind: KindValidation, Code: code, Message: message}
}

func Validation(fields ...FieldError) *Error {
	return &Error{
		Kind:    KindValidation,
		Code:    "validation_failed",
		Message: "One or more fields are invalid.",
		Fields:  fields,
	}
}
