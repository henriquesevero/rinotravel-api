package ids

import "github.com/google/uuid"

func New() string {
	return uuid.Must(uuid.NewV7()).String()
}

func IsValid(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.String() == id
}
