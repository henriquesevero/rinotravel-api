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
	"rinotravel-api/internal/itinerary"
	itineraryapi "rinotravel-api/internal/itinerary/httpapi"
	itinerarymongo "rinotravel-api/internal/itinerary/mongorepo"
	"rinotravel-api/internal/platform/config"
	"rinotravel-api/internal/platform/httpx"
	"rinotravel-api/internal/server"
	"rinotravel-api/internal/trip"
	tripapi "rinotravel-api/internal/trip/httpapi"
	tripmongo "rinotravel-api/internal/trip/mongorepo"
	"rinotravel-api/internal/user"
	usermongo "rinotravel-api/internal/user/mongorepo"
)

const (
	startupTimeout = 30 * time.Second
	authRateWindow = time.Minute
)

type Deps struct {
	Logger *slog.Logger
	Config config.Config
	DB     *mongo.Database
}

func Build(ctx context.Context, d Deps) ([]server.Module, error) {
	ctx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()

	users := usermongo.New(d.DB)
	sessions := authmongo.NewSessionRepository(d.DB)
	guard := authapi.NewGuard(auth.NewAuthenticate(sessions))
	trips := tripmongo.New(d.DB)
	authz := trip.NewAuthorizer(trips)

	for _, ensure := range []func(context.Context) error{users.EnsureIndexes, sessions.EnsureIndexes, trips.EnsureIndexes} {
		if err := ensure(ctx); err != nil {
			return nil, err
		}
	}

	authModule, err := authModule(d, users, sessions, guard)
	if err != nil {
		return nil, err
	}
	itineraryModule, err := itineraryModule(ctx, d, guard, authz)
	if err != nil {
		return nil, err
	}

	return []server.Module{authModule, tripModule(d, trips, users, guard), itineraryModule}, nil
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

func tripModule(d Deps, trips *tripmongo.Repository, users *usermongo.Repository, guard authapi.Guard) server.Module {
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

func itineraryModule(ctx context.Context, d Deps, guard authapi.Guard, authz *trip.Authorizer) (server.Module, error) {
	days := itinerarymongo.NewDayStore(d.DB)
	items := itinerarymongo.NewItemStore(d.DB)
	if err := days.EnsureIndexes(ctx, itinerarymongo.DayIndexes()...); err != nil {
		return nil, err
	}
	if err := items.EnsureIndexes(ctx); err != nil {
		return nil, err
	}
	return itineraryapi.New(itineraryapi.Deps{
		Logger:   d.Logger,
		Guard:    guard,
		Days:     itinerary.NewDays(days, items, authz),
		Items:    itinerary.NewItems(items, days, authz),
		Timeline: itinerary.NewTimeline(days, items, authz),
	}), nil
}
