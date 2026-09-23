// Package full assembles every feature over in-memory repositories, fake storage and fake external
// providers, so cross-feature behavior (sync, timeline, redaction) can be tested through HTTP.
package full

import (
	"context"
	"sync/atomic"
	"testing"

	"time"

	"rinotravel-api/internal/apitest"
	"rinotravel-api/internal/booking"
	bookingapi "rinotravel-api/internal/booking/httpapi"
	"rinotravel-api/internal/daymap"
	daymapapi "rinotravel-api/internal/daymap/httpapi"
	"rinotravel-api/internal/document"
	"rinotravel-api/internal/document/documenttest"
	documentapi "rinotravel-api/internal/document/httpapi"
	"rinotravel-api/internal/expense"
	expenseapi "rinotravel-api/internal/expense/httpapi"
	"rinotravel-api/internal/google"
	"rinotravel-api/internal/itinerary"
	itineraryapi "rinotravel-api/internal/itinerary/httpapi"
	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/place"
	placeapi "rinotravel-api/internal/place/httpapi"
	"rinotravel-api/internal/platform/httpx"
	"rinotravel-api/internal/quota/quotatest"
	"rinotravel-api/internal/resource/resourcetest"
	"rinotravel-api/internal/routing"
	"rinotravel-api/internal/server"
	"rinotravel-api/internal/syncengine"
	syncapi "rinotravel-api/internal/syncengine/httpapi"
	"rinotravel-api/internal/syncengine/synctest"
	tripapi "rinotravel-api/internal/trip/httpapi"
)

type Stack struct {
	*apitest.Env
	Storage *documenttest.Storage
	Places  *FakePlaces
	Routes  *FakeRoutes
	Maps    *FakeMaps
	Log     *synctest.Log
	// Quota is the monthly Google allowance the fakes are metered against, like in production.
	Quota *quotatest.Counter
}

// GoogleLimit is the monthly allowance per API in the harness; tests use Quota.Set to spend it.
const GoogleLimit = 1000

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
	Routes []routing.Route
	Err    error
	Calls  atomic.Int32
}

func (f *FakeRoutes) Name() string { return "fake" }

func (f *FakeRoutes) Compute(context.Context, routing.RouteRequest) ([]routing.Route, error) {
	f.Calls.Add(1)
	return f.Routes, f.Err
}

// FakeMaps stands in for the static map service and remembers what it was asked to draw.
type FakeMaps struct {
	Image kernel.MapImage
	Err   error
	Pins  []place.PinSpec
	Days  []daymap.DaySpec
}

func (f *FakeMaps) RenderPin(_ context.Context, spec place.PinSpec) (kernel.MapImage, error) {
	f.Pins = append(f.Pins, spec)
	return f.Image, f.Err
}

func (f *FakeMaps) RenderDay(_ context.Context, spec daymap.DaySpec) (kernel.MapImage, error) {
	f.Days = append(f.Days, spec)
	return f.Image, f.Err
}

func New(t *testing.T) *Stack {
	t.Helper()
	s := &Stack{Storage: documenttest.NewStorage(), Places: &FakePlaces{}, Routes: &FakeRoutes{}, Maps: &FakeMaps{}, Log: synctest.NewLog(), Quota: &quotatest.Counter{}}

	s.Env = apitest.New(t, func(e apitest.Env) []server.Module {
		authz := e.Authz
		days := resourcetest.New(itinerary.DayBase).WithUnique(func(a, b itinerary.Day) bool { return a.Date == b.Date })
		items := resourcetest.New(itinerary.ItemBase)
		places := resourcetest.New(place.PlaceBase)
		restaurants := resourcetest.New(place.RestaurantBase)
		flights := resourcetest.New(booking.FlightBase)
		hotels := resourcetest.New(booking.HotelBase)
		tickets := resourcetest.New(booking.TicketBase)
		expenses := resourcetest.New(expense.Base)
		limits := resourcetest.New(expense.LimitBase)
		payments := resourcetest.New(expense.PaymentBase)
		documents := resourcetest.New(document.Base)

		placesUC := place.NewPlaces(places, authz)
		placeH := placeapi.New(placeapi.Deps{
			Logger: e.Logger, Guard: e.Guard, Places: placesUC, Restaurants: place.NewRestaurants(restaurants, authz),
			Search: place.NewSearchPlaces(google.NewMeteredPlaces(s.Places, s.Quota, GoogleLimit, e.Logger), e.Logger), SearchLimiter: httpx.NewRateLimiter(1000, time.Minute, httpx.ClientIP(false)),
			Maps:       place.NewLocationMaps(google.NewMeteredMaps(s.Maps, s.Quota, GoogleLimit, e.Logger), authz, e.Logger),
			MapLimiter: httpx.NewRateLimiter(1000, time.Minute, httpx.ClientIP(false)),
		})
		documentService := document.NewDocuments(documents, authz, s.Storage, "test", e.Logger)
		documentH := documentapi.New(documentapi.Deps{Logger: e.Logger, Guard: e.Guard, Documents: documentService})
		bookingH := bookingapi.New(bookingapi.Deps{Logger: e.Logger, Guard: e.Guard, Flights: booking.NewFlights(flights, authz), Hotels: booking.NewHotels(hotels, authz), Tickets: booking.NewTickets(tickets, authz, documentService)})
		expenseH := expenseapi.New(expenseapi.Deps{Logger: e.Logger, Guard: e.Guard, Expenses: expense.NewExpenses(expenses, authz), Limits: expense.NewLimits(limits, authz), Payments: expense.NewPayments(payments, authz)})
		routes := google.NewMeteredRoutes(s.Routes, s.Quota, GoogleLimit, e.Logger)
		itineraryH := itineraryapi.New(itineraryapi.Deps{
			Logger: e.Logger, Guard: e.Guard, Days: itinerary.NewDays(days, items, authz), Items: itinerary.NewItems(items, days, authz),
			Places: placesUC,
			Timeline: itinerary.NewTimeline(days, items, authz,
				place.NewTimelineSource(restaurants), booking.NewTimelineSource(flights, hotels, tickets)),
		})

		var sources []syncengine.Source
		sources = append(sources, itineraryH.SyncSources()...)
		sources = append(sources, placeH.SyncSources()...)
		sources = append(sources, bookingH.SyncSources()...)
		sources = append(sources, expenseH.SyncSources()...)
		sources = append(sources, documentH.SyncSource())
		if tripsHandler, ok := tripHandler(e); ok {
			sources = append(sources, tripsHandler.SyncSource(e.Trips))
		}
		engine := syncengine.NewEngine(authz, s.Log, synctest.Direct{}, sources...)

		dayH := daymapapi.New(e.Logger, e.Guard,
			daymap.NewService(routes, google.NewMeteredMaps(s.Maps, s.Quota, GoogleLimit, e.Logger), e.Authz, e.Logger),
			httpx.NewRateLimiter(1000, time.Minute, httpx.ClientIP(false)))

		return []server.Module{placeH, bookingH, expenseH, documentH, itineraryH, dayH, syncapi.New(e.Logger, e.Guard, engine)}
	})
	return s
}

// tripHandler rebuilds the trip handler solely to obtain its sync source.
func tripHandler(e apitest.Env) (*tripapi.Handler, bool) {
	return apitest.TripHandler(e), true
}
