package apihttp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"uuid"

	"github.com/go-chi/chi/v5"

	"github.com/anesthetised/couchcast/internal/access"
	"github.com/anesthetised/couchcast/internal/auth"
	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/repository"
)

// BugReportStore persists problem reports and reads what the server adds
// to them.
type BugReportStore interface {
	CreateBugReport(ctx context.Context, b *entity.BugReport, frame []byte) (*entity.BugReport, error)
	GetBugReport(ctx context.Context, id uuid.UUID) (*entity.BugReport, error)
	ListBugReports(ctx context.Context, q repository.BugReportQuery) ([]entity.BugReport, error)
	GetBugReportFrame(ctx context.Context, id uuid.UUID) ([]byte, error)
	ResolveBugReport(ctx context.Context, id, by uuid.UUID, note string) error
	GetMedia(ctx context.Context, id uuid.UUID) (*entity.Media, error)
	LatestIngestJob(ctx context.Context, mediaID uuid.UUID) (*repository.IngestJobState, error)
}

const (
	bugClientMaxBytes = 64 << 10
	bugFrameMaxBytes  = 300 << 10
	bugBodyMaxBytes   = 640 << 10 // client + base64 frame + the rest
	bugDescriptionMax = 2000
	bugNoteMax        = 500
	bugPageSize       = 50
)

type bugReportRequest struct {
	Category    string          `json:"category"`
	Description string          `json:"description"`
	RoomSlug    string          `json:"roomSlug"`
	MediaID     *uuid.UUID      `json:"mediaId"`
	Client      json.RawMessage `json:"client"`
	Frame       string          `json:"frame"` // base64 JPEG, optional
}

// bugServerState is what the server knew when the report arrived.
type bugServerState struct {
	Version    string          `json:"version"`
	ReceivedAt time.Time       `json:"receivedAt"`
	UserAgent  string          `json:"userAgent"`
	Room       *bugServerRoom  `json:"room,omitempty"`
	Media      *bugServerMedia `json:"media,omitempty"`
}

type bugServerRoom struct {
	ID         uuid.UUID       `json:"id"`
	Slug       string          `json:"slug"`
	Visibility string          `json:"visibility"`
	Role       entity.RoomRole `json:"role"`
	Loaded     bool            `json:"loaded"`
	Live       any             `json:"live,omitempty"` // room.DebugState when loaded
}

type bugServerMedia struct {
	ID         uuid.UUID          `json:"id"`
	SourceKey  string             `json:"sourceKey"`
	Status     entity.MediaStatus `json:"status"`
	Error      string             `json:"error,omitempty"`
	DurationMs int64              `json:"durationMs"`
	SizeBytes  int64              `json:"sizeBytes"`
	Renditions []entity.Rendition `json:"renditions"`
	Subtitles  []entity.Subtitle  `json:"subtitles"`
	S3Prefix   string             `json:"s3Prefix"`
	Job        any                `json:"job,omitempty"`
}

// handleCreateBugReport accepts a signed-in viewer's report, checks it
// against the room they name, and adds the server's half.
func (s *Server) handleCreateBugReport(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())
	var req bugReportRequest
	if !decodeJSONLimit(w, r, &req, bugBodyMaxBytes) {
		return
	}
	category := entity.BugCategory(req.Category)
	if !category.Valid() {
		writeError(w, http.StatusBadRequest, "category must be playback, sync, subtitles, chat or other")
		return
	}
	desc := strings.TrimSpace(req.Description)
	if utf8.RuneCountInString(desc) > bugDescriptionMax {
		writeError(w, http.StatusBadRequest, "description must be at most 2000 characters")
		return
	}
	client := bytes.TrimSpace(req.Client)
	if len(client) == 0 {
		client = []byte("{}")
	}
	if len(client) > bugClientMaxBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "technical details are too large")
		return
	}
	if client[0] != '{' || !json.Valid(client) {
		writeError(w, http.StatusBadRequest, "client must be a JSON object")
		return
	}
	var frame []byte
	if req.Frame != "" {
		b, err := base64.StdEncoding.DecodeString(req.Frame)
		switch {
		case err != nil:
			writeError(w, http.StatusBadRequest, "frame must be base64")
			return
		case len(b) > bugFrameMaxBytes:
			writeError(w, http.StatusRequestEntityTooLarge, "frame is too large")
			return
		case !bytes.HasPrefix(b, []byte{0xFF, 0xD8, 0xFF}):
			writeError(w, http.StatusBadRequest, "frame must be a JPEG")
			return
		}
		frame = b
	}
	if desc == "" && len(client) <= 2 && frame == nil {
		writeError(w, http.StatusBadRequest, "describe the problem or include technical details")
		return
	}
	if !allowUser(w, s.deps.BugReportLimiter, user.ID) {
		return
	}

	state := bugServerState{Version: version, ReceivedAt: time.Now().UTC(), UserAgent: r.UserAgent()}
	report := &entity.BugReport{UserID: &user.ID, Category: category, Description: desc, Client: client}

	if req.RoomSlug != "" {
		room, err := s.deps.Rooms.GetRoomBySlug(r.Context(), req.RoomSlug)
		switch {
		case errors.Is(err, repository.ErrNotFound):
			writeError(w, http.StatusNotFound, "room not found")
			return
		case err != nil:
			s.internalError(w, r, "load room", err)
			return
		}
		actor, err := s.actorFor(r.Context(), room, user)
		if err != nil {
			s.internalError(w, r, "load membership", err)
			return
		}
		rc := roomCtx{room: room, actor: actor}
		if !s.require(w, rc, access.ViewRoom) {
			return
		}
		report.RoomID = &room.ID
		sr := &bugServerRoom{ID: room.ID, Slug: room.Slug, Visibility: string(room.Visibility), Role: actor.Role()}
		if s.deps.RoomDebug != nil {
			if live, ok := s.deps.RoomDebug(room.ID); ok {
				sr.Loaded, sr.Live = true, live
			}
		}
		state.Room = sr
	}

	if req.MediaID != nil {
		m, err := s.deps.BugReports.GetMedia(r.Context(), *req.MediaID)
		switch {
		case errors.Is(err, repository.ErrNotFound):
			// Deleted meanwhile: keep the report, drop the link.
		case err != nil:
			s.internalError(w, r, "load media", err)
			return
		default:
			report.MediaID = &m.ID
			sm := &bugServerMedia{
				ID: m.ID, SourceKey: m.SourceKey, Status: m.Status, Error: m.Error, DurationMs: m.DurationMs,
				SizeBytes: m.SizeBytes, Renditions: m.Renditions, Subtitles: m.Subtitles, S3Prefix: m.S3Prefix,
			}
			if job, err := s.deps.BugReports.LatestIngestJob(r.Context(), m.ID); err == nil {
				sm.Job = job
			} else if !errors.Is(err, repository.ErrNotFound) {
				s.deps.Logger.WarnContext(r.Context(), "bug report: ingest job", "error", err)
			}
			state.Media = sm
		}
	}

	server, err := json.Marshal(state)
	if err != nil {
		s.internalError(w, r, "encode server state", err)
		return
	}
	report.Server = server
	saved, err := s.deps.BugReports.CreateBugReport(r.Context(), report, frame)
	if err != nil {
		s.internalError(w, r, "create bug report", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": saved.ID})
}

// --- admin ---------------------------------------------------------------------

type bugReportResponse struct {
	ID          uuid.UUID          `json:"id"`
	Author      string             `json:"author"`
	RoomSlug    string             `json:"roomSlug,omitempty"`
	MediaID     *uuid.UUID         `json:"mediaId,omitempty"`
	MediaTitle  string             `json:"mediaTitle,omitempty"`
	Category    entity.BugCategory `json:"category"`
	Description string             `json:"description"`
	Client      json.RawMessage    `json:"client"`
	Server      json.RawMessage    `json:"server"`
	HasFrame    bool               `json:"hasFrame"`
	CreatedAt   time.Time          `json:"createdAt"`
	ResolvedAt  *time.Time         `json:"resolvedAt"`
	Note        string             `json:"note"`
}

type bugReportPage struct {
	Reports []bugReportResponse `json:"reports"`
	// NextBefore pages further back (RFC 3339); empty at the end.
	NextBefore string `json:"nextBefore"`
}

func toBugReportResponse(b *entity.BugReport) bugReportResponse {
	return bugReportResponse{
		ID: b.ID, Author: b.Username, RoomSlug: b.RoomSlug, MediaID: b.MediaID, MediaTitle: b.MediaTitle,
		Category: b.Category, Description: b.Description, Client: b.Client, Server: b.Server,
		HasFrame: b.HasFrame, CreatedAt: b.CreatedAt, ResolvedAt: b.ResolvedAt, Note: b.Note,
	}
}

// handleAdminBugReports lists ?status=open|resolved, newest first,
// paging with ?before=<RFC 3339>.
func (s *Server) handleAdminBugReports(w http.ResponseWriter, r *http.Request) {
	q := repository.BugReportQuery{Resolved: r.URL.Query().Get("status") == "resolved", Limit: bugPageSize + 1}
	if v := r.URL.Query().Get("before"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "before must be an RFC 3339 time")
			return
		}
		q.Before = t
	}
	list, err := s.deps.BugReports.ListBugReports(r.Context(), q)
	if err != nil {
		s.internalError(w, r, "list bug reports", err)
		return
	}
	page := bugReportPage{Reports: make([]bugReportResponse, 0, len(list))}
	if len(list) > bugPageSize {
		list = list[:bugPageSize]
		page.NextBefore = list[len(list)-1].CreatedAt.Format(time.RFC3339Nano)
	}
	for i := range list {
		page.Reports = append(page.Reports, toBugReportResponse(&list[i]))
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) bugReportID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "report not found")
		return uuid.Nil(), false
	}
	return id, true
}

func (s *Server) handleAdminBugReport(w http.ResponseWriter, r *http.Request) {
	id, ok := s.bugReportID(w, r)
	if !ok {
		return
	}
	b, err := s.deps.BugReports.GetBugReport(r.Context(), id)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, "report not found")
		return
	}
	if err != nil {
		s.internalError(w, r, "get bug report", err)
		return
	}
	writeJSON(w, http.StatusOK, toBugReportResponse(b))
}

func (s *Server) handleAdminBugReportFrame(w http.ResponseWriter, r *http.Request) {
	id, ok := s.bugReportID(w, r)
	if !ok {
		return
	}
	frame, err := s.deps.BugReports.GetBugReportFrame(r.Context(), id)
	if errors.Is(err, repository.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.internalError(w, r, "get bug report frame", err)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = w.Write(frame) //nolint:gosec // JPEG checked at intake, served as image/jpeg with nosniff (securityHeaders)
}

type resolveBugRequest struct {
	Note string `json:"note"`
}

func (s *Server) handleAdminResolveBugReport(w http.ResponseWriter, r *http.Request) {
	admin := auth.UserFrom(r.Context())
	id, ok := s.bugReportID(w, r)
	if !ok {
		return
	}
	var req resolveBugRequest
	if r.ContentLength != 0 && !decodeJSON(w, r, &req) {
		return
	}
	note := strings.TrimSpace(req.Note)
	if utf8.RuneCountInString(note) > bugNoteMax {
		writeError(w, http.StatusBadRequest, "note must be at most 500 characters")
		return
	}
	err := s.deps.BugReports.ResolveBugReport(r.Context(), id, admin.ID, note)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, "report not found or already resolved")
		return
	}
	if err != nil {
		s.internalError(w, r, "resolve bug report", err)
		return
	}
	s.audit(r, "bug.resolve", "bug_report", id.String(), nil, map[string]any{"note": note})
	w.WriteHeader(http.StatusNoContent)
}
