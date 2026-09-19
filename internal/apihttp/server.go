// Package apihttp wires the HTTP surface of the web server: the JSON API
// under /api/v1, operational endpoints and the embedded single-page app.
package apihttp

import (
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/anesthetised/couchcast/internal/metrics"
)

// Pinger reports whether a dependency is reachable. *pgxpool.Pool satisfies it.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Deps lists everything the router needs. Later phases add repositories,
// the room manager and the WebSocket hub here.
type Deps struct {
	Logger  *slog.Logger
	DB      Pinger
	Metrics *metrics.Metrics
	Static  fs.FS // built SPA; may contain only a placeholder in development
}

// Server owns the chi router.
type Server struct {
	deps Deps
	mux  chi.Router
}

// New builds the router. Route registration lives here so that the full
// URL space is visible in one place.
func New(deps Deps) *Server {
	s := &Server{deps: deps, mux: chi.NewRouter()}

	r := s.mux
	r.Use(middleware.RequestID)
	r.Use(requestLogger(deps.Logger))
	r.Use(middleware.Recoverer)
	r.Use(deps.Metrics.HTTPMiddleware)

	r.Get("/healthz", s.handleHealthz)
	r.Method(http.MethodGet, "/metrics", deps.Metrics.Handler())

	r.Route("/api/v1", func(r chi.Router) {
		r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusNotFound, "not found")
		})
		r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		})
	})

	r.NotFound(spaHandler(deps.Static))

	return s
}

// Handler returns the router as an http.Handler.
func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := s.deps.DB.Ping(ctx); err != nil {
		s.deps.Logger.WarnContext(ctx, "healthz: database unreachable", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "degraded", "database": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": version})
}

type errorResponse struct {
	Error string `json:"error"`
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// requestLogger logs one line per request through slog, with the request id
// so that log lines and error responses can be correlated.
func requestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			next.ServeHTTP(ww, r)

			logger.LogAttrs(r.Context(), slog.LevelInfo, "http request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", ww.Status()),
				slog.Int("bytes", ww.BytesWritten()),
				slog.Duration("duration", time.Since(start)),
				slog.String("request_id", middleware.GetReqID(r.Context())),
				slog.String("remote", r.RemoteAddr),
			)
		})
	}
}

// version is reported by /healthz; the binary sets it at startup.
var version = "dev"

// SetVersion records the build version reported by /healthz.
func SetVersion(v string) { version = v }
