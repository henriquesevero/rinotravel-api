package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"

	"rinotravel-api/internal/auth"
	"rinotravel-api/internal/auth/argon2id"
	authapi "rinotravel-api/internal/auth/httpapi"
	authmongo "rinotravel-api/internal/auth/mongorepo"
	"rinotravel-api/internal/platform/config"
	"rinotravel-api/internal/platform/httpx"
	"rinotravel-api/internal/platform/logging"
	"rinotravel-api/internal/platform/mongodb"
	"rinotravel-api/internal/server"
	"rinotravel-api/internal/user"
	usermongo "rinotravel-api/internal/user/mongorepo"

	"rinotravel-api/internal/trip"
	tripapi "rinotravel-api/internal/trip/httpapi"
	tripmongo "rinotravel-api/internal/trip/mongorepo"
)

const (
	shutdownTimeout = 10 * time.Second
	startupTimeout  = 30 * time.Second
	authRateWindow  = time.Minute
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}

	logger := logging.New(os.Stdout, cfg.LogLevel, cfg.Env != config.Development)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	mongoClient, err := mongodb.Connect(ctx, cfg.MongoDBURI)
	if err != nil {
		return err
	}
	defer func() {
		if err := mongodb.Disconnect(mongoClient); err != nil {
			logger.Error("disconnect from mongodb", slog.Any("error", err))
		}
	}()

	db := mongoClient.Database(cfg.MongoDBDatabase)
	users := usermongo.New(db)
	sessions := authmongo.NewSessionRepository(db)
	guard := authapi.NewGuard(auth.NewAuthenticate(sessions))

	authAPI, err := newAuthAPI(ctx, logger, cfg, users, sessions, guard)
	if err != nil {
		return err
	}
	tripAPI, err := newTripAPI(ctx, logger, db, users, guard)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: server.NewHandler(server.Options{
			Logger:             logger,
			CORSAllowedOrigins: cfg.CORSAllowedOrigins,
			Modules:            []server.Module{authAPI, tripAPI},
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	serverErr := make(chan error, 1)
	go func() { serverErr <- srv.ListenAndServe() }()
	logger.Info("server started", slog.String("addr", cfg.HTTPAddr), slog.String("env", string(cfg.Env)))

	select {
	case err := <-serverErr:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}

	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("shutdown http server: %w", err)
	}
	return nil
}

func newAuthAPI(ctx context.Context, logger *slog.Logger, cfg config.Config, users *usermongo.Repository, sessions *authmongo.SessionRepository, guard authapi.Guard) (*authapi.Handler, error) {
	indexCtx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()
	if err := users.EnsureIndexes(indexCtx); err != nil {
		return nil, err
	}
	if err := sessions.EnsureIndexes(indexCtx); err != nil {
		return nil, err
	}

	hasher := argon2id.New(argon2id.DefaultParams)
	login, err := auth.NewLogin(users, sessions, hasher)
	if err != nil {
		return nil, err
	}

	return authapi.New(authapi.Deps{
		Logger:      logger,
		Guard:       guard,
		RateLimiter: httpx.NewRateLimiter(cfg.AuthRateLimit, authRateWindow, httpx.ClientIP(cfg.TrustProxy)),
		Register:    auth.NewRegister(users, sessions, hasher, cfg.RegistrationCode),
		Login:       login,
		Logout:      auth.NewLogout(sessions),
		GetUser:     user.NewGetUser(users),
	}), nil
}

func newTripAPI(ctx context.Context, logger *slog.Logger, db *mongo.Database, users *usermongo.Repository, guard authapi.Guard) (*tripapi.Handler, error) {
	trips := tripmongo.New(db)

	indexCtx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()
	if err := trips.EnsureIndexes(indexCtx); err != nil {
		return nil, err
	}

	return tripapi.New(tripapi.Deps{
		Logger:            logger,
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
	}), nil
}
