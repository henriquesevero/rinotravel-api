// Package full assembles every feature over in-memory repositories, fake storage and fake external
// providers, so cross-feature behavior (sync, timeline, redaction) can be tested through HTTP.
package full

import (
	"context"
	"testing"

	"rinotravel-api/internal/apitest"
	"rinotravel-api/internal/booking"
	bookingapi "rinotravel-api/internal/booking/httpapi"
	"rinotravel-api/internal/document"
	"rinotravel-api/internal/document/documenttest"
	documentapi "rinotravel-api/internal/document/httpapi"
	"rinotravel-api/internal/itinerary"
	itineraryapi "rinotravel-api/internal/itinerary/httpapi"
	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/place"
	placeapi "rinotravel-api/internal/place/httpapi"
	"rinotravel-api/internal/platform/httpx"
	"rinotravel-api/internal/resource/resourcetest"
	"rinotravel-api/internal/server"
	"rinotravel-api/internal/syncengine"
	syncapi "rinotravel-api/internal/syncengine/httpapi"
	"rinotravel-api/internal/syncengine/synctest"
	"rinotravel-api/internal/transfer"
	transferapi "rinotravel-api/internal/transfer/httpapi"
	tripapi "rinotravel-api/internal/trip/httpapi"
	"time"
)

type Stack struct {
	*apitest.Env
	Storage *documenttest.Storage
	Places  *FakePlaces
	Routes  *FakeRoutes
	Log     *synctest.Log
}

type FakePlaces struct {
	Results []place.Candidate
	Err     error
	Queries []string
}

func (f *FakePlaces) Search(_ context.Context, q string, _ *kernel.Coordinates, _ string) ([]place.Candidate, error) {
	f.Queries = append(f.Queries, q)
	return f.Results, f.Err
}

func (f *FakePlaces) Details(context.Context, string, string) (place.Candidate, error) {
	return place.Candidate{}, f.Err
}

type FakeRoutes struct {
	Routes []transfer.Route
	Err    error
}

func (f *FakeRoutes) Name() string { return "fake" }

func (f *FakeRoutes) Compute(context.Context, transfer.RouteRequest) ([]transfer.Route, error) {
	return f.Routes, f.Err
}

func New(t *testing.T) *Stack {
	t.Helper()
	s := &Stack{Storage: documenttest.NewStorage(), Places: &FakePlaces{}, Routes: &FakeRoutes{}, Log: synctest.NewLog()}

	s.Env = apitest.New(t, func(e apitest.Env) []server.Module {
		authz := e.Authz
		days := resourcetest.New(itinerary.DayBase).WithUnique(func(a, b itinerary.Day) bool { return a.Date == b.Date })
		items := resourcetest.New(itinerary.ItemBase)
		places := resourcetest.New(place.PlaceBase)
		restaurants := resourcetest.New(place.RestaurantBase)
		flights := resourcetest.New(booking.FlightBase)
		hotels := resourcetest.New(booking.HotelBase)
		transfers := resourcetest.New(transfer.Base)
		documents := resourcetest.New(document.Base)

		placesUC := place.NewPlaces(places, authz)
		placeH := placeapi.New(placeapi.Deps{
			Logger: e.Logger, Guard: e.Guard, Places: placesUC, Restaurants: place.NewRestaurants(restaurants, authz),
			Search: place.NewSearchPlaces(s.Places, e.Logger), SearchLimiter: httpx.NewRateLimiter(1000, time.Minute, httpx.ClientIP(false)),
		})
		bookingH := bookingapi.New(bookingapi.Deps{Logger: e.Logger, Guard: e.Guard, Flights: booking.NewFlights(flights, authz), Hotels: booking.NewHotels(hotels, authz)})
		transferH := transferapi.New(transferapi.Deps{
			Logger: e.Logger, Guard: e.Guard, Transfers: transfer.NewTransfers(transfers, authz),
			Planner: transfer.NewPlanner(s.Routes, authz, e.Logger),
		})
		documentH := documentapi.New(documentapi.Deps{Logger: e.Logger, Guard: e.Guard, Documents: document.NewDocuments(documents, authz, s.Storage, "test", e.Logger)})
		itineraryH := itineraryapi.New(itineraryapi.Deps{
			Logger: e.Logger, Guard: e.Guard, Days: itinerary.NewDays(days, items, authz), Items: itinerary.NewItems(items, days, authz),
			Places: placesUC,
			Timeline: itinerary.NewTimeline(days, items, authz,
				place.NewTimelineSource(restaurants), booking.NewTimelineSource(flights, hotels), transfer.NewTimelineSource(transfers)),
		})

		var sources []syncengine.Source
		sources = append(sources, itineraryH.SyncSources()...)
		sources = append(sources, placeH.SyncSources()...)
		sources = append(sources, bookingH.SyncSources()...)
		sources = append(sources, transferH.SyncSources()...)
		sources = append(sources, documentH.SyncSource())
		if tripsHandler, ok := tripHandler(e); ok {
			sources = append(sources, tripsHandler.SyncSource(e.Trips))
		}
		engine := syncengine.NewEngine(authz, s.Log, synctest.Direct{}, sources...)

		return []server.Module{placeH, bookingH, transferH, documentH, itineraryH, syncapi.New(e.Logger, e.Guard, engine)}
	})
	return s
}

// tripHandler rebuilds the trip handler solely to obtain its sync source.
func tripHandler(e apitest.Env) (*tripapi.Handler, bool) {
	return apitest.TripHandler(e), true
}
