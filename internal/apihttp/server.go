// Package apihttp wires the HTTP surface of the web server: the JSON API
// under /api/v1, operational endpoints and the embedded single-page app.
package apihttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"uuid"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/anesthetised/couchcast/internal/access"
	"github.com/anesthetised/couchcast/internal/auth"
	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/mediastore"
	"github.com/anesthetised/couchcast/internal/metrics"
	"github.com/anesthetised/couchcast/internal/ratelimit"
)

// Pinger reports whether a dependency is reachable. *pgxpool.Pool satisfies it.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Admitter checks a URL without touching the network (ingest.Service).
type Admitter interface {
	Key(rawURL string) (string, error)
}

// LiveQueue adds a URL to a room's queue through the room manager, so the
// admission, rate limit and autoplay rules of a normal queue.add apply.
type LiveQueue interface {
	QueueAdd(ctx context.Context, roomID uuid.UUID, actor access.Actor, rawURL string) error
}

// WebSocketServer upgrades a room connection (implemented by hub.Hub).
type WebSocketServer interface {
	Serve(w http.ResponseWriter, r *http.Request, rm *entity.Room, actor access.Actor)
}

// Deps lists everything the router needs. Later phases add repositories,
// the room manager and the WebSocket hub here.
type Deps struct {
	Logger  *slog.Logger
	DB      Pinger
	Metrics *metrics.Metrics
	Static  fs.FS // built SPA; may contain only a placeholder in development

	Users    UserStore
	Rooms    RoomStore
	Admin    AdminStore // nil disables reports and the admin API (tests)
	Sessions *auth.Sessions

	// Directory, Live and Signer serve the public rooms listing; nil
	// Directory disables the route (tests).
	Directory DirectoryStore
	Live      LiveRooms
	Signer    *mediastore.Signer

	// Admit validates a first video URL before a room is created and
	// LiveQueue enqueues it through the live room. Nil disables firstUrl.
	Admit     Admitter
	LiveQueue LiveQueue
	// Prober serves GET /media/probe for the add form; nil disables it.
	Prober Prober
	// InviteLinks serves invite links and /join; nil disables them.
	InviteLinks InviteLinkStore

	// MediaObjects deletes packaged media when an administrator removes it.
	MediaObjects MediaDeleter
	// RoomsLoaded reports rooms held in memory for the stats endpoint.
	RoomsLoaded func() int

	// Media serves /media/{id}/{file}; nil disables the route (tests).
	Media http.Handler
	// WS serves the room WebSocket; nil disables the route (tests).
	WS WebSocketServer

	// Hooks let the live room layer react to REST changes. All optional.
	OnBan          func(roomID, userID uuid.UUID)
	OnLeave        func(roomID, userID uuid.UUID)
	OnRoomChanged  func(roomID uuid.UUID)
	OnRoomDeleted  func(roomID uuid.UUID)
	OnUserBanned   func(userID uuid.UUID)
	OnMediaDeleted func(mediaID uuid.UUID)

	// AuthLimiter is applied per client IP to register/login; LoginLimiter
	// per username to login. Either may be nil to disable.
	AuthLimiter  *ratelimit.Limiter
	LoginLimiter *ratelimit.Limiter
	TrustProxy   bool

	// Per-user budgets for actions that create rows. Nil disables.
	RoomCreateLimiter *ratelimit.Limiter
	InviteLimiter     *ratelimit.Limiter
	ReportLimiter     *ratelimit.Limiter
	ProbeLimiter      *ratelimit.Limiter
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
	if deps.Media != nil {
		r.Handle("/media/*", deps.Media)
	}

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(deps.Sessions.Middleware(deps.Logger))
		r.Use(auth.CheckOrigin)
		r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusNotFound, "not found")
		})
		r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		})

		r.Route("/auth", func(r chi.Router) {
			r.Group(func(r chi.Router) {
				if deps.AuthLimiter != nil {
					r.Use(deps.AuthLimiter.Middleware(ratelimit.ClientIP(deps.TrustProxy)))
				}
				r.Post("/register", s.handleRegister)
				r.Post("/login", s.handleLogin)
			})
			r.Post("/logout", s.handleLogout)
			r.Get("/me", s.handleMe)
		})

		r.Route("/rooms", func(r chi.Router) {
			r.With(auth.RequireUser).Post("/", s.handleCreateRoom)
			if deps.Directory != nil {
				r.Get("/", s.handleDirectory)
			}
			r.Route("/{slug}", func(r chi.Router) {
				r.Get("/", s.handleGetRoom)
				r.Get("/members", s.handleListMembers)
				if deps.WS != nil {
					r.Get("/ws", s.handleWS)
				}

				r.Group(func(r chi.Router) {
					r.Use(auth.RequireUser)
					r.Patch("/", s.handleUpdateRoom)
					r.Delete("/", s.handleDeleteRoom)
					r.Put("/moderators/{username}", s.handleAddModerator)
					r.Delete("/moderators/{username}", s.handleRemoveModerator)
					r.Delete("/members/{username}", s.handleRemoveMember)
					r.Post("/owner", s.handleTransferOwnership)
					r.Get("/bans", s.handleListBans)
					r.Put("/bans/{username}", s.handleBan)
					r.Delete("/bans/{username}", s.handleUnban)
					r.Post("/invites", s.handleCreateInvite)
					if deps.InviteLinks != nil {
						r.Post("/invite-links", s.handleCreateInviteLink)
						r.Get("/invite-links", s.handleListInviteLinks)
						r.Delete("/invite-links/{id}", s.handleRevokeInviteLink)
					}
				})
			})
		})

		if deps.InviteLinks != nil {
			r.Get("/join/{token}", s.handleJoinPreview)
			r.With(auth.RequireUser).Post("/join/{token}", s.handleJoin)
		}

		r.Group(func(r chi.Router) {
			r.Use(auth.RequireUser)
			r.Patch("/me", s.handleUpdateMe)
			r.Post("/me/password", s.handleChangePassword)
			r.Get("/me/rooms", s.handleMyRooms)
			r.Get("/users", s.handleSearchUsers)
			r.Get("/invites", s.handleMyInvites)
			r.Post("/invites/{id}/accept", s.handleAcceptInvite)
			r.Post("/invites/{id}/decline", s.handleDeclineInvite)
			if deps.Admin != nil {
				r.Post("/media/{id}/reports", s.handleCreateReport)
			}
			if deps.Prober != nil {
				r.Get("/media/probe", s.handleProbe)
			}
		})

		if deps.Admin != nil {
			r.Route("/admin", func(r chi.Router) {
				r.Use(auth.RequireAdmin)
				r.Get("/stats", s.handleAdminStats)
				r.Get("/users", s.handleAdminUsers)
				r.Post("/users/{id}/ban", s.handleAdminBanUser)
				r.Post("/users/{id}/unban", s.handleAdminUnbanUser)
				r.Get("/rooms", s.handleAdminRooms)
				r.Delete("/rooms/{slug}", s.handleAdminDeleteRoom)
				r.Get("/reports", s.handleAdminReports)
				r.Post("/reports/{id}/dismiss", s.handleAdminDismissReports)
				r.Delete("/media/{id}", s.handleAdminDeleteMedia)
				r.Get("/blocklist", s.handleAdminBlocklist)
				r.Post("/blocklist", s.handleAdminBlock)
				r.Delete("/blocklist/*", s.handleAdminUnblock)
				r.Get("/audit", s.handleAdminAudit)
			})
		}
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

// maxBodyBytes bounds JSON request bodies; nothing in the API is large.
const maxBodyBytes = 64 << 10

// decodeJSON parses the body into v, writing a 400 and returning false on
// malformed input.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	err := json.NewDecoder(r.Body).Decode(v)
	switch {
	case err == nil:
		return true
	case errors.Is(err, io.EOF):
		writeError(w, http.StatusBadRequest, "request body is required")
	default:
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
		} else {
			writeError(w, http.StatusBadRequest, "malformed JSON: "+err.Error())
		}
	}
	return false
}

// allowUser charges one event to the user's budget on the limiter and
// answers 429 when it is exhausted. A nil limiter always allows.
func allowUser(w http.ResponseWriter, l *ratelimit.Limiter, userID uuid.UUID) bool {
	if l == nil || l.Allow(userID.String()) {
		return true
	}
	w.Header().Set("Retry-After", "60")
	writeError(w, http.StatusTooManyRequests, "you are doing that too often; try again later")
	return false
}

// internalError logs the cause with the request id and hides it from the
// client.
func (s *Server) internalError(w http.ResponseWriter, r *http.Request, what string, err error) {
	s.deps.Logger.ErrorContext(r.Context(), what, "error", err, "request_id", middleware.GetReqID(r.Context()))
	writeError(w, http.StatusInternalServerError, "internal error")
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
