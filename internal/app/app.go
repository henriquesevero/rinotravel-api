// Package app is the composition root: it builds every repository, use case and HTTP module and
// wires them together. Nothing else in the codebase constructs its own dependencies.
package app

import (
	"context"
	"log/slog"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"

	"rinotravel-api/internal/auth"
	"rinotravel-api/internal/auth/argon2id"
	authapi "rinotravel-api/internal/auth/httpapi"
	authmongo "rinotravel-api/internal/auth/mongorepo"
	"rinotravel-api/internal/booking"
	bookingapi "rinotravel-api/internal/booking/httpapi"
	bookingmongo "rinotravel-api/internal/booking/mongorepo"
	"rinotravel-api/internal/checklist"
	checklistapi "rinotravel-api/internal/checklist/httpapi"
	checklistmongo "rinotravel-api/internal/checklist/mongorepo"
	"rinotravel-api/internal/daymap"
	daymapapi "rinotravel-api/internal/daymap/httpapi"
	"rinotravel-api/internal/document"
	"rinotravel-api/internal/document/gridfs"
	documentapi "rinotravel-api/internal/document/httpapi"
	documentmongo "rinotravel-api/internal/document/mongorepo"
	"rinotravel-api/internal/expense"
	expenseapi "rinotravel-api/internal/expense/httpapi"
	expensemongo "rinotravel-api/internal/expense/mongorepo"
	"rinotravel-api/internal/google"
	"rinotravel-api/internal/itinerary"
	itineraryapi "rinotravel-api/internal/itinerary/httpapi"
	itinerarymongo "rinotravel-api/internal/itinerary/mongorepo"
	"rinotravel-api/internal/place"
	placeapi "rinotravel-api/internal/place/httpapi"
	placemongo "rinotravel-api/internal/place/mongorepo"
	"rinotravel-api/internal/platform/config"
	"rinotravel-api/internal/platform/httpx"
	quotamongo "rinotravel-api/internal/quota/mongorepo"
	"rinotravel-api/internal/routing"
	"rinotravel-api/internal/server"
	"rinotravel-api/internal/syncengine"
	syncapi "rinotravel-api/internal/syncengine/httpapi"
	"rinotravel-api/internal/syncengine/mongolog"
	"rinotravel-api/internal/trip"
	tripapi "rinotravel-api/internal/trip/httpapi"
	tripmongo "rinotravel-api/internal/trip/mongorepo"
	"rinotravel-api/internal/user"
	usermongo "rinotravel-api/internal/user/mongorepo"
)

const (
	startupTimeout = 30 * time.Second
	authRateWindow = time.Minute
	// providerRateLimit caps paid external lookups per client and minute.
	providerRateLimit = 30
)

type Deps struct {
	Logger *slog.Logger
	Config config.Config
	DB     *mongo.Database
}

// registry collects what each feature contributes while the application is assembled.
type registry struct {
	modules []server.Module
	sources []syncengine.Source
}

func (r *registry) add(module server.Module, sources ...syncengine.Source) {
	r.modules = append(r.modules, module)
	r.sources = append(r.sources, sources...)
}

func Build(ctx context.Context, d Deps) ([]server.Module, error) {
	ctx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()

	users := usermongo.New(d.DB)
	sessions := authmongo.NewSessionRepository(d.DB)
	guard := authapi.NewGuard(auth.NewAuthenticate(sessions))
	trips := tripmongo.New(d.DB)
	authz := trip.NewAuthorizer(trips)
	mutations := mongolog.New(d.DB)

	days := itinerarymongo.NewDayStore(d.DB)
	items := itinerarymongo.NewItemStore(d.DB)
	checklistItems := checklistmongo.NewItemStore(d.DB)
	places := placemongo.NewPlaceStore(d.DB)
	restaurants := placemongo.NewRestaurantStore(d.DB)
	flights := bookingmongo.NewFlightStore(d.DB)
	hotels := bookingmongo.NewHotelStore(d.DB)
	tickets := bookingmongo.NewTicketStore(d.DB)
	documents := documentmongo.NewStore(d.DB)
	expenses := expensemongo.NewExpenseStore(d.DB)
	limits := expensemongo.NewLimitStore(d.DB)
	payments := expensemongo.NewPaymentStore(d.DB)

	for _, ensure := range []func(context.Context) error{
		users.EnsureIndexes, sessions.EnsureIndexes, trips.EnsureIndexes, mutations.EnsureIndexes,
		func(ctx context.Context) error { return days.EnsureIndexes(ctx, itinerarymongo.DayIndexes()...) },
		func(ctx context.Context) error { return items.EnsureIndexes(ctx) },
		func(ctx context.Context) error { return checklistItems.EnsureIndexes(ctx) },
		func(ctx context.Context) error { return places.EnsureIndexes(ctx) },
		func(ctx context.Context) error { return restaurants.EnsureIndexes(ctx) },
		func(ctx context.Context) error { return flights.EnsureIndexes(ctx) },
		func(ctx context.Context) error { return hotels.EnsureIndexes(ctx) },
		func(ctx context.Context) error { return tickets.EnsureIndexes(ctx) },
		func(ctx context.Context) error { return documents.EnsureIndexes(ctx) },
		func(ctx context.Context) error { return expenses.EnsureIndexes(ctx) },
		func(ctx context.Context) error { return limits.EnsureIndexes(ctx, expensemongo.LimitIndexes()...) },
		func(ctx context.Context) error { return payments.EnsureIndexes(ctx, expensemongo.PaymentIndexes()...) },
	} {
		if err := ensure(ctx); err != nil {
			return nil, err
		}
	}

	var reg registry

	authMod, err := authModule(d, users, sessions, guard)
	if err != nil {
		return nil, err
	}
	reg.add(authMod)

	tripsHandler := tripHandler(d, trips, users, guard)
	reg.add(tripsHandler, tripsHandler.SyncSource(trips))

	placesUC := place.NewPlaces(places, authz)
	restaurantsUC := place.NewRestaurants(restaurants, authz)
	placesDeps := placeapi.Deps{Logger: d.Logger, Guard: guard, Places: placesUC, Restaurants: restaurantsUC}
	if d.Config.GoogleMapsAPIKey != "" {
		var placeProvider place.PlaceProvider = google.NewPlaces(d.Config.GoogleMapsAPIKey)
		var routeProvider routing.RouteProvider = google.NewRoutes(d.Config.GoogleMapsAPIKey)
		var mapRenderer any = google.NewStaticMaps(d.Config.GoogleMapsAPIKey)
		if limit := d.Config.GoogleMonthlyLimit; limit > 0 {
			counter := quotamongo.New(d.DB)
			placeProvider = google.NewMeteredPlaces(placeProvider, counter, limit, d.Logger)
			routeProvider = google.NewMeteredRoutes(routeProvider, counter, limit, d.Logger)
			mapRenderer = google.NewMeteredMaps(mapRenderer, counter, limit, d.Logger)
			d.Logger.Info("google calls are capped", slog.Int("monthly_limit_per_api", limit))
		} else {
			d.Logger.Warn("google calls are NOT capped: GOOGLE_MONTHLY_LIMIT is 0, usage past the free allowance is billed")
		}
		routeProvider = google.NewEnglishFallback(routeProvider)
		placesDeps.Search = place.NewSearchPlaces(placeProvider, d.Logger)
		placesDeps.SearchLimiter = httpx.NewRateLimiter(providerRateLimit, time.Minute, httpx.ClientIP(d.Config.TrustProxy))
		placesDeps.Maps = place.NewLocationMaps(mapRenderer.(place.PinRenderer), authz, d.Logger)
		placesDeps.MapLimiter = httpx.NewRateLimiter(providerRateLimit, time.Minute, httpx.ClientIP(d.Config.TrustProxy))
		reg.add(daymapapi.New(d.Logger, guard,
			daymap.NewService(routeProvider, mapRenderer.(daymap.ImageRenderer), authz, d.Logger),
			httpx.NewRateLimiter(providerRateLimit, time.Minute, httpx.ClientIP(d.Config.TrustProxy))))
	} else {
		d.Logger.Info("place search and route planning disabled: GOOGLE_MAPS_API_KEY is not set")
	}
	placesHandler := placeapi.New(placesDeps)
	reg.add(placesHandler, placesHandler.SyncSources()...)

	expenseHandler := expenseapi.New(expenseapi.Deps{
		Logger: d.Logger, Guard: guard,
		Expenses: expense.NewExpenses(expenses, authz), Limits: expense.NewLimits(limits, authz),
		Payments: expense.NewPayments(payments, authz),
	})
	reg.add(expenseHandler, expenseHandler.SyncSources()...)

	// Tickets can point at a file in the documents; without documents there is nothing to point at.
	var documentService *document.Documents
	if d.Config.StorageSigningSecret != "" {
		storage, err := gridfs.New(d.DB, gridfs.Config{Secret: d.Config.StorageSigningSecret, PublicURL: d.Config.APIPublicURL}, d.Logger)
		if err != nil {
			return nil, err
		}
		documentService = document.NewDocuments(documents, authz, storage, string(d.Config.Env), d.Logger)
		documentHandler := documentapi.New(documentapi.Deps{Logger: d.Logger, Guard: guard, Documents: documentService})
		reg.add(documentHandler, documentHandler.SyncSource())
		reg.add(storage)
	} else {
		d.Logger.Info("documents disabled: STORAGE_SIGNING_SECRET is not set")
	}

	bookingHandler := bookingapi.New(bookingapi.Deps{
		Logger: d.Logger, Guard: guard,
		Flights: booking.NewFlights(flights, authz), Hotels: booking.NewHotels(hotels, authz),
		Tickets: booking.NewTickets(tickets, authz, documentReader(documentService)),
	})
	reg.add(bookingHandler, bookingHandler.SyncSources()...)

	itineraryHandler := itineraryapi.New(itineraryapi.Deps{
		Logger: d.Logger,
		Guard:  guard,
		Days:   itinerary.NewDays(days, items, authz),
		Items:  itinerary.NewItems(items, days, authz),
		Places: placesUC,
		Timeline: itinerary.NewTimeline(days, items, authz,
			place.NewTimelineSource(restaurants), booking.NewTimelineSource(flights, hotels, tickets)),
	})
	reg.add(itineraryHandler, itineraryHandler.SyncSources()...)

	checklistHandler := checklistapi.New(checklistapi.Deps{
		Logger: d.Logger, Guard: guard, Items: checklist.NewItems(checklistItems, authz),
	})
	reg.add(checklistHandler, checklistHandler.SyncSource())

	engine := syncengine.NewEngine(authz, mutations, mongolog.NewTransactor(d.DB), reg.sources...)
	reg.add(syncapi.New(d.Logger, guard, engine))
	return reg.modules, nil
}

func authModule(d Deps, users *usermongo.Repository, sessions *authmongo.SessionRepository, guard authapi.Guard) (server.Module, error) {
	hasher := argon2id.New(argon2id.DefaultParams)
	login, err := auth.NewLogin(users, sessions, hasher)
	if err != nil {
		return nil, err
	}
	return authapi.New(authapi.Deps{
		Logger:      d.Logger,
		Guard:       guard,
		RateLimiter: httpx.NewRateLimiter(d.Config.AuthRateLimit, authRateWindow, httpx.ClientIP(d.Config.TrustProxy)),
		Register:    auth.NewRegister(users, sessions, hasher, d.Config.RegistrationCode),
		Login:       login,
		Logout:      auth.NewLogout(sessions),
		GetUser:     user.NewGetUser(users),
	}), nil
}

func tripHandler(d Deps, trips *tripmongo.Repository, users *usermongo.Repository, guard authapi.Guard) *tripapi.Handler {
	return tripapi.New(tripapi.Deps{
		Logger:            d.Logger,
		Guard:             guard,
		CreateTrip:        trip.NewCreateTrip(trips),
		GetTrip:           trip.NewGetTrip(trips),
		ListTrips:         trip.NewListTrips(trips),
		UpdateTrip:        trip.NewUpdateTrip(trips),
		DeleteTrip:        trip.NewDeleteTrip(trips),
		AddMember:         trip.NewAddMember(trips, users),
		ListMembers:       trip.NewListMembers(trips, users),
		ChangeMemberRole:  trip.NewChangeMemberRole(trips, users),
		RemoveMember:      trip.NewRemoveMember(trips),
		TransferOwnership: trip.NewTransferOwnership(trips),
	})
}

// documentReader keeps a missing document service a nil interface, which the ticket rules test for.
func documentReader(documents *document.Documents) booking.DocumentReader {
	if documents == nil {
		return nil
	}
	return documents
}
