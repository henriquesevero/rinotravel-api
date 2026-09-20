package place

import (
	"context"
	"log/slog"
	"strings"
	"unicode/utf8"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/user"
)

// Candidate is a place found by a provider. It is a suggestion the user may copy into their own
// wishlist; only the provider's id is kept as a link for later enrichment.
type Candidate struct {
	ProviderID  string
	Name        string
	Address     string
	Coordinates *kernel.Coordinates
	Types       []string
}

// PlaceProvider is the port to a places directory. The Google adapter implements it, and nothing
// in the use cases depends on Google's types.
type PlaceProvider interface {
	Search(ctx context.Context, query string, near *kernel.Coordinates, language string) ([]Candidate, error)
	Details(ctx context.Context, providerID, language string) (Candidate, error)
}

type SearchInput struct {
	Query     string
	Latitude  *float64
	Longitude *float64
	Language  string
}

type SearchPlaces struct {
	provider PlaceProvider
	logger   *slog.Logger
}

func NewSearchPlaces(provider PlaceProvider, logger *slog.Logger) *SearchPlaces {
	return &SearchPlaces{provider: provider, logger: logger}
}

func (s *SearchPlaces) Execute(ctx context.Context, _ user.ID, in SearchInput) ([]Candidate, error) {
	var v kernel.Validator
	query := strings.TrimSpace(in.Query)
	v.Check(utf8.RuneCountInString(query) >= 2 && utf8.RuneCountInString(query) <= 200, "q", "must have between 2 and 200 characters")
	loc := v.Location("near", kernel.LocationInput{Latitude: in.Latitude, Longitude: in.Longitude})
	if err := v.Err(); err != nil {
		return nil, err
	}

	found, err := s.provider.Search(ctx, query, loc.Coordinates, language(in.Language))
	if err != nil {
		s.logger.ErrorContext(ctx, "place search failed", slog.Any("error", err))
		return nil, apperror.Unavailable("provider_unavailable", "Place search is temporarily unavailable.")
	}
	return found, nil
}

func language(l string) string {
	if l == "" {
		return "en"
	}
	return l
}
