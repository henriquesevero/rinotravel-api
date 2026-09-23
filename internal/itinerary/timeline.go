package itinerary

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/resource"
	"rinotravel-api/internal/trip"
	"rinotravel-api/internal/user"
)

// EntrySource lets other domains (flights, hotels, reservations) contribute lines to
// the timeline without the itinerary knowing about them or duplicating their data.
type EntrySource interface {
	Entries(ctx context.Context, tripID string) ([]kernel.TimelineEntry, error)
}

type DayView struct {
	Date kernel.Date
	// Day is nil for dates that only exist because something (a flight, say) happens on them.
	Day     *Day
	Entries []kernel.TimelineEntry
}

type Itinerary struct {
	Days []DayView
}

type Timeline struct {
	days    resource.Repo[Day]
	items   resource.Repo[Item]
	sources []EntrySource
	authz   resource.Authorizer
}

func NewTimeline(days resource.Repo[Day], items resource.Repo[Item], authz resource.Authorizer, sources ...EntrySource) *Timeline {
	return &Timeline{days: days, items: items, authz: authz, sources: sources}
}

func (t *Timeline) Get(ctx context.Context, actor user.ID, tripID trip.ID) (Itinerary, trip.Role, error) {
	access, err := t.authz.Authorize(ctx, tripID, actor, trip.ActionRead)
	if err != nil {
		return Itinerary{}, "", err
	}
	id := string(tripID)

	days, err := t.days.List(ctx, id)
	if err != nil {
		return Itinerary{}, "", fmt.Errorf("list days: %w", err)
	}
	items, err := t.items.List(ctx, id)
	if err != nil {
		return Itinerary{}, "", fmt.Errorf("list items: %w", err)
	}

	byDate := map[kernel.Date]*DayView{}
	dayDate := map[string]kernel.Date{}
	view := func(date kernel.Date) *DayView {
		if v, ok := byDate[date]; ok {
			return v
		}
		v := &DayView{Date: date}
		byDate[date] = v
		return v
	}

	for i := range days {
		day := days[i]
		view(day.Date).Day = &day
		dayDate[day.ID] = day.Date
	}
	for _, item := range items {
		if date, ok := dayDate[item.DayID]; ok {
			v := view(date)
			v.Entries = append(v.Entries, item.Entry(date))
		}
	}
	for _, source := range t.sources {
		entries, err := source.Entries(ctx, id)
		if err != nil {
			return Itinerary{}, "", fmt.Errorf("timeline source: %w", err)
		}
		for _, entry := range entries {
			v := view(entry.Day)
			v.Entries = append(v.Entries, entry)
		}
	}

	out := Itinerary{Days: make([]DayView, 0, len(byDate))}
	for _, v := range byDate {
		slices.SortStableFunc(v.Entries, compareEntries)
		out.Days = append(out.Days, *v)
	}
	slices.SortFunc(out.Days, func(a, b DayView) int { return cmp.Compare(a.Date, b.Date) })
	return out, access.Role, nil
}

// compareEntries orders by real time first (so a 22:00 Sao Paulo event sorts against a Lisbon
// one correctly), then puts untimed entries after timed ones in their manual order.
func compareEntries(a, b kernel.TimelineEntry) int {
	switch {
	case a.Start != nil && b.Start != nil:
		if c := a.Start.Instant.Compare(b.Start.Instant); c != 0 {
			return c
		}
	case a.Start != nil:
		return -1
	case b.Start != nil:
		return 1
	}
	if c := cmp.Compare(a.Position, b.Position); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Title, b.Title); c != 0 {
		return c
	}
	return cmp.Compare(a.ID, b.ID)
}
