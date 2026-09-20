package google

import (
	"context"
	"net/http"
	"net/url"

	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/place"
)

const placeFields = "places.id,places.displayName,places.formattedAddress,places.location,places.types"

const searchRadiusMeters = 50000.0

type Places struct {
	c client
}

func NewPlaces(apiKey string) *Places {
	return &Places{c: newClient(apiKey, defaultPlacesBase, nil)}
}

// NewPlacesWithBase points the adapter at another server; tests use it with a fake Google.
func NewPlacesWithBase(apiKey, base string, httpClient *http.Client) *Places {
	return &Places{c: newClient(apiKey, base, httpClient)}
}

type placeDTO struct {
	ID          string `json:"id"`
	DisplayName struct {
		Text string `json:"text"`
	} `json:"displayName"`
	FormattedAddress string `json:"formattedAddress"`
	Location         *struct {
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	} `json:"location"`
	Types []string `json:"types"`
}

func (d placeDTO) candidate() place.Candidate {
	c := place.Candidate{ProviderID: d.ID, Name: d.DisplayName.Text, Address: d.FormattedAddress, Types: d.Types}
	if d.Location != nil {
		c.Coordinates = &kernel.Coordinates{Lat: d.Location.Latitude, Lng: d.Location.Longitude}
	}
	return c
}

func (p *Places) Search(ctx context.Context, query string, near *kernel.Coordinates, language string) ([]place.Candidate, error) {
	body := map[string]any{"textQuery": query, "languageCode": language, "maxResultCount": 10}
	if near != nil {
		body["locationBias"] = map[string]any{"circle": map[string]any{
			"center": map[string]float64{"latitude": near.Lat, "longitude": near.Lng},
			"radius": searchRadiusMeters,
		}}
	}
	var out struct {
		Places []placeDTO `json:"places"`
	}
	if err := p.c.do(ctx, http.MethodPost, "/v1/places:searchText", placeFields, body, &out); err != nil {
		return nil, err
	}
	found := make([]place.Candidate, 0, len(out.Places))
	for _, dto := range out.Places {
		found = append(found, dto.candidate())
	}
	return found, nil
}

func (p *Places) Details(ctx context.Context, providerID, language string) (place.Candidate, error) {
	var dto placeDTO
	path := "/v1/places/" + url.PathEscape(providerID) + "?languageCode=" + url.QueryEscape(language)
	if err := p.c.do(ctx, http.MethodGet, path, "id,displayName,formattedAddress,location,types", nil, &dto); err != nil {
		return place.Candidate{}, err
	}
	return dto.candidate(), nil
}
