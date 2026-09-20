package ids_test

import (
	"testing"

	"github.com/google/uuid"

	"rinotravel-api/internal/platform/ids"
)

func TestNew_ReturnsUniqueUUIDv7(t *testing.T) {
	first, second := ids.New(), ids.New()

	for _, id := range []string{first, second} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed.Version() != 7 {
			t.Errorf("New() = %q, want a valid UUIDv7 (err %v)", id, err)
		}
	}
	if first == second {
		t.Errorf("New() returned %q twice", first)
	}
	if first >= second {
		t.Errorf("ids are not time-ordered: %q >= %q", first, second)
	}
}

func TestIsValid(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{ids.New(), true},
		{"550e8400-e29b-41d4-a716-446655440000", true},
		{"", false},
		{"not-a-uuid", false},
		{"550E8400-E29B-41D4-A716-446655440000", false},
		{"550e8400e29b41d4a716446655440000", false},
		{"urn:uuid:550e8400-e29b-41d4-a716-446655440000", false},
		{"{550e8400-e29b-41d4-a716-446655440000}", false},
		{"550e8400-e29b-41d4-a716-446655440000 ", false},
	}

	for _, tt := range tests {
		if got := ids.IsValid(tt.id); got != tt.want {
			t.Errorf("IsValid(%q) = %v, want %v", tt.id, got, tt.want)
		}
	}
}
