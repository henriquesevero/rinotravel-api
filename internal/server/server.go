package server

import (
	"log/slog"
	"net/http"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/platform/httpx"
)

type Module interface {
	Mount(mux *http.ServeMux)
}

type Options struct {
	Logger             *slog.Logger
	CORSAllowedOrigins []string
	Modules            []Module
}

func NewHandler(opts Options) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", health)
	for _, m := range opts.Modules {
		m.Mount(mux)
	}
	mux.Handle("/", httpx.Handle(opts.Logger, notFound))

	return httpx.Chain(mux,
		httpx.RequestID,
		httpx.AccessLog(opts.Logger),
		httpx.CORS(opts.CORSAllowedOrigins),
		httpx.Recover(opts.Logger),
	)
}

func health(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func notFound(http.ResponseWriter, *http.Request) error {
	return apperror.NotFound("route_not_found", "The requested resource does not exist.")
}
