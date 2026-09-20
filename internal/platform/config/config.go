package config

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
)

type Environment string

const (
	Development Environment = "development"
	Staging     Environment = "staging"
	Production  Environment = "production"
)

type Config struct {
	Env                Environment
	HTTPAddr           string
	LogLevel           slog.Level
	CORSAllowedOrigins []string
	TrustProxy         bool
	AuthRateLimit      int
	RegistrationCode   string
	MongoDBURI         string
	MongoDBDatabase    string
}

const (
	minRegistrationCodeLength = 12
	defaultAuthRateLimit      = 10
)

func Load(getenv func(string) string) (Config, error) {
	var (
		cfg  Config
		errs []error
	)

	switch env := Environment(getenv("APP_ENV")); env {
	case Development, Staging, Production:
		cfg.Env = env
	default:
		errs = append(errs, fmt.Errorf("APP_ENV must be one of %s, %s, %s", Development, Staging, Production))
	}

	port := valueOrDefault(getenv("PORT"), "8080")
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		errs = append(errs, errors.New("PORT must be a number between 1 and 65535"))
	} else {
		cfg.HTTPAddr = ":" + port
	}

	if err := cfg.LogLevel.UnmarshalText([]byte(valueOrDefault(getenv("LOG_LEVEL"), "info"))); err != nil {
		errs = append(errs, errors.New("LOG_LEVEL must be one of debug, info, warn, error"))
	}

	cfg.CORSAllowedOrigins = splitList(getenv("CORS_ALLOWED_ORIGINS"))

	if raw := getenv("TRUST_PROXY"); raw != "" {
		trust, err := strconv.ParseBool(raw)
		if err != nil {
			errs = append(errs, errors.New("TRUST_PROXY must be true or false"))
		}
		cfg.TrustProxy = trust
	}

	cfg.AuthRateLimit = defaultAuthRateLimit
	if raw := getenv("AUTH_RATE_LIMIT"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 {
			errs = append(errs, errors.New("AUTH_RATE_LIMIT must be a positive number"))
		} else {
			cfg.AuthRateLimit = limit
		}
	}

	cfg.RegistrationCode = getenv("REGISTRATION_CODE")
	if len(cfg.RegistrationCode) < minRegistrationCodeLength {
		errs = append(errs, fmt.Errorf("REGISTRATION_CODE is required and must have at least %d characters", minRegistrationCodeLength))
	}

	cfg.MongoDBURI = getenv("MONGODB_URI")
	if cfg.MongoDBURI == "" {
		errs = append(errs, errors.New("MONGODB_URI is required"))
	}

	cfg.MongoDBDatabase = getenv("MONGODB_DATABASE")
	if cfg.MongoDBDatabase == "" {
		errs = append(errs, errors.New("MONGODB_DATABASE is required"))
	}

	if err := errors.Join(errs...); err != nil {
		return Config{}, fmt.Errorf("invalid configuration: %w", err)
	}
	return cfg, nil
}

func valueOrDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func splitList(value string) []string {
	var items []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}
