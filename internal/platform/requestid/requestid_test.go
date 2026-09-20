package requestid_test

import (
	"context"
	"strings"
	"testing"

	"rinotravel-api/internal/platform/requestid"
)

func TestNew_ReturnsValidUniqueIDs(t *testing.T) {
	first, second := requestid.New(), requestid.New()

	if !requestid.IsValid(first) {
		t.Errorf("New() = %q, want a valid id", first)
	}
	if first == second {
		t.Errorf("New() returned the same id twice: %q", first)
	}
}

func TestIsValid(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{"abc-123_DEF", true},
		{strings.Repeat("a", 64), true},
		{"", false},
		{strings.Repeat("a", 65), false},
		{"has space", false},
		{"line\nbreak", false},
		{"emoji-😀", false},
	}

	for _, tt := range tests {
		if got := requestid.IsValid(tt.id); got != tt.want {
			t.Errorf("IsValid(%q) = %v, want %v", tt.id, got, tt.want)
		}
	}
}

func TestContext(t *testing.T) {
	if got := requestid.FromContext(context.Background()); got != "" {
		t.Errorf("FromContext(empty) = %q, want empty", got)
	}

	ctx := requestid.WithContext(context.Background(), "req-1")
	if got := requestid.FromContext(ctx); got != "req-1" {
		t.Errorf("FromContext() = %q, want req-1", got)
	}
}
