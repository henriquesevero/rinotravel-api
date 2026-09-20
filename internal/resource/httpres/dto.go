package httpres

import (
	"fmt"
	"net/http"
	"time"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/platform/ids"
)

// Meta is the sync-ready envelope every resource response starts with.
type Meta struct {
	ID        string    `json:"id"`
	TripID    string    `json:"tripId"`
	Version   int64     `json:"version"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func MetaOf(b kernel.Base) Meta {
	return Meta{ID: b.ID, TripID: b.TripID, Version: b.Version, CreatedAt: b.CreatedAt, UpdatedAt: b.UpdatedAt}
}

type ZonedTimeDTO struct {
	DateTime string `json:"dateTime"`
	Timezone string `json:"timezone"`
}

func ZonedOf(z *kernel.ZonedTime) *ZonedTimeDTO {
	if z == nil {
		return nil
	}
	return &ZonedTimeDTO{DateTime: z.LocalString(), Timezone: string(z.Zone)}
}

type LocationDTO struct {
	Name      string   `json:"name,omitempty"`
	Address   string   `json:"address,omitempty"`
	Latitude  *float64 `json:"latitude,omitempty"`
	Longitude *float64 `json:"longitude,omitempty"`
}

func LocationOf(l kernel.Location) *LocationDTO {
	if l.IsZero() {
		return nil
	}
	dto := &LocationDTO{Name: l.Name, Address: l.Address}
	if l.Coordinates != nil {
		lat, lng := l.Coordinates.Lat, l.Coordinates.Lng
		dto.Latitude, dto.Longitude = &lat, &lng
	}
	return dto
}

type MoneyDTO struct {
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
}

func MoneyOf(m *kernel.Money) *MoneyDTO {
	if m == nil {
		return nil
	}
	return &MoneyDTO{Amount: m.Amount, Currency: string(m.Currency)}
}

type TimelineEntryDTO struct {
	Kind     string        `json:"kind"`
	ID       string        `json:"id"`
	Title    string        `json:"title"`
	Subtitle string        `json:"subtitle,omitempty"`
	Status   string        `json:"status,omitempty"`
	Start    *ZonedTimeDTO `json:"start,omitempty"`
	End      *ZonedTimeDTO `json:"end,omitempty"`
}

func EntryOf(e kernel.TimelineEntry) TimelineEntryDTO {
	return TimelineEntryDTO{
		Kind: e.Kind, ID: e.ID, Title: e.Title, Subtitle: e.Subtitle, Status: e.Status,
		Start: ZonedOf(e.Start), End: ZonedOf(e.End),
	}
}

func PathID(r *http.Request, name string) (string, error) {
	id := r.PathValue(name)
	if !ids.IsValid(id) {
		return "", apperror.BadRequest("invalid_id", fmt.Sprintf("The %s in the path must be a lowercase UUID.", name))
	}
	return id, nil
}

type List[T any] struct {
	Items []T `json:"items"`
}
