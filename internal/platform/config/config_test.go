package config_test

import (
	"log/slog"
	"strings"
	"testing"

	"rinotravel-api/internal/platform/config"
)

func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func validEnv() map[string]string {
	return map[string]string{
		"APP_ENV":           "development",
		"MONGODB_URI":       "mongodb://localhost:27017",
		"MONGODB_DATABASE":  "rinotravel_dev",
		"REGISTRATION_CODE": "dev-registration-code",
	}
}

func TestLoad_AppliesDefaults(t *testing.T) {
	cfg, err := config.Load(env(validEnv()))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Env != config.Development {
		t.Errorf("Env = %q, want %q", cfg.Env, config.Development)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want :8080", cfg.HTTPAddr)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want info", cfg.LogLevel)
	}
	if len(cfg.CORSAllowedOrigins) != 0 {
		t.Errorf("CORSAllowedOrigins = %v, want empty", cfg.CORSAllowedOrigins)
	}
	if cfg.TrustProxy {
		t.Error("TrustProxy = true, want false by default")
	}
	if cfg.AuthRateLimit != 10 {
		t.Errorf("AuthRateLimit = %d, want 10 by default", cfg.AuthRateLimit)
	}
}

func TestLoad_ReadsAllValues(t *testing.T) {
	values := validEnv()
	values["APP_ENV"] = "production"
	values["PORT"] = "9000"
	values["LOG_LEVEL"] = "debug"
	values["CORS_ALLOWED_ORIGINS"] = " https://a.example , https://b.example,,"
	values["TRUST_PROXY"] = "true"
	values["AUTH_RATE_LIMIT"] = "250"

	cfg, err := config.Load(env(values))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Env != config.Production || cfg.HTTPAddr != ":9000" || cfg.LogLevel != slog.LevelDebug {
		t.Errorf("unexpected config: %+v", cfg)
	}
	if got := strings.Join(cfg.CORSAllowedOrigins, "|"); got != "https://a.example|https://b.example" {
		t.Errorf("CORSAllowedOrigins = %q", got)
	}
	if cfg.MongoDBURI != values["MONGODB_URI"] || cfg.MongoDBDatabase != values["MONGODB_DATABASE"] {
		t.Errorf("unexpected mongodb config: %+v", cfg)
	}
	if !cfg.TrustProxy || cfg.AuthRateLimit != 250 || cfg.RegistrationCode != values["REGISTRATION_CODE"] {
		t.Errorf("unexpected auth config: %+v", cfg)
	}
}

func TestLoad_RejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(map[string]string)
		wantMsg string
	}{
		{"missing env", func(v map[string]string) { delete(v, "APP_ENV") }, "APP_ENV"},
		{"unknown env", func(v map[string]string) { v["APP_ENV"] = "prod" }, "APP_ENV"},
		{"non numeric port", func(v map[string]string) { v["PORT"] = "abc" }, "PORT"},
		{"port out of range", func(v map[string]string) { v["PORT"] = "70000" }, "PORT"},
		{"unknown log level", func(v map[string]string) { v["LOG_LEVEL"] = "verbose" }, "LOG_LEVEL"},
		{"missing mongodb uri", func(v map[string]string) { delete(v, "MONGODB_URI") }, "MONGODB_URI"},
		{"non numeric auth rate limit", func(v map[string]string) { v["AUTH_RATE_LIMIT"] = "many" }, "AUTH_RATE_LIMIT"},
		{"zero auth rate limit", func(v map[string]string) { v["AUTH_RATE_LIMIT"] = "0" }, "AUTH_RATE_LIMIT"},
		{"non boolean trust proxy", func(v map[string]string) { v["TRUST_PROXY"] = "maybe" }, "TRUST_PROXY"},
		{"missing registration code", func(v map[string]string) { delete(v, "REGISTRATION_CODE") }, "REGISTRATION_CODE"},
		{"short registration code", func(v map[string]string) { v["REGISTRATION_CODE"] = "short" }, "REGISTRATION_CODE"},
		{"missing mongodb database", func(v map[string]string) { delete(v, "MONGODB_DATABASE") }, "MONGODB_DATABASE"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values := validEnv()
			tt.mutate(values)

			_, err := config.Load(env(values))
			if err == nil {
				t.Fatal("Load() error = nil, want error")
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("error %q does not mention %s", err, tt.wantMsg)
			}
		})
	}
}

func TestLoad_ReportsAllProblemsAtOnce(t *testing.T) {
	_, err := config.Load(env(nil))
	if err == nil {
		t.Fatal("Load() error = nil, want error")
	}

	for _, name := range []string{"APP_ENV", "MONGODB_URI", "MONGODB_DATABASE", "REGISTRATION_CODE"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not mention %s", err, name)
		}
	}
}

func TestLoad_ReadsOptionalIntegrations(t *testing.T) {
	values := validEnv()
	cfg, err := config.Load(env(values))
	if err != nil || cfg.StorageSigningSecret != "" || cfg.GoogleMapsAPIKey != "" {
		t.Fatalf("defaults: %+v, %v", cfg, err)
	}

	values["STORAGE_SIGNING_SECRET"] = strings.Repeat("s", 32)
	values["API_PUBLIC_URL"] = "https://api.example.com"
	values["GOOGLE_MAPS_API_KEY"] = "gkey"
	cfg, err = config.Load(env(values))
	if err != nil || cfg.StorageSigningSecret != values["STORAGE_SIGNING_SECRET"] || cfg.APIPublicURL != "https://api.example.com" || cfg.GoogleMapsAPIKey != "gkey" {
		t.Errorf("configured: %+v, %v", cfg, err)
	}
}

func TestLoad_StorageSecretRules(t *testing.T) {
	values := validEnv()
	values["STORAGE_SIGNING_SECRET"] = "too-short"
	if _, err := config.Load(env(values)); err == nil || !strings.Contains(err.Error(), "STORAGE_SIGNING_SECRET") {
		t.Errorf("a short secret must be rejected, got %v", err)
	}

	values["STORAGE_SIGNING_SECRET"] = strings.Repeat("s", 32)
	cfg, err := config.Load(env(values))
	if err != nil || cfg.APIPublicURL != "http://localhost:8080" {
		t.Errorf("development derives the public URL: %+v, %v", cfg, err)
	}

	values["APP_ENV"] = "production"
	if _, err := config.Load(env(values)); err == nil || !strings.Contains(err.Error(), "API_PUBLIC_URL") {
		t.Errorf("production must state its public URL, got %v", err)
	}
}
