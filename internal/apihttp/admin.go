package apihttp

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"uuid"

	"github.com/go-chi/chi/v5"

	"github.com/anesthetised/couchcast/internal/auth"
	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/repository"
)

// AdminStore is what the report and admin handlers need.
type AdminStore interface {
	GetMedia(ctx context.Context, id uuid.UUID) (*entity.Media, error)
	CreateReport(ctx context.Context, mediaID, reporterID uuid.UUID, reason entity.ReportReason, comment string) error
	ListReportedMedia(ctx context.Context, limit int) ([]entity.ReportedMedia, error)
	ResolveReports(ctx context.Context, mediaID, resolvedBy uuid.UUID) (int64, error)
	DeleteMedia(ctx context.Context, id uuid.UUID) error
	BlockSource(ctx context.Context, sourceKey, reason string, createdBy *uuid.UUID) error
	ListBlocklist(ctx context.Context) ([]entity.BlocklistEntry, error)
	UnblockSource(ctx context.Context, sourceKey string) error
	Stats(ctx context.Context) (*entity.Stats, error)
	ListUsers(ctx context.Context, query string, limit int) ([]entity.AdminUser, error)
	ListRooms(ctx context.Context, query string, limit int) ([]entity.Room, error)
	ListAudit(ctx context.Context, roomID *uuid.UUID, limit int) ([]entity.AuditEntry, error)
	UsernamesByID(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error)
	BanUser(ctx context.Context, id, bannedBy uuid.UUID, reason string, at time.Time) error
	UnbanUser(ctx context.Context, id uuid.UUID) error
	GetRoomBySlug(ctx context.Context, slug string) (*entity.Room, error)
	DeleteRoom(ctx context.Context, id uuid.UUID) error
}

// MediaDeleter removes packaged objects (mediastore.Store).
type MediaDeleter interface {
	DeletePrefix(ctx context.Context, prefix string) error
}

// --- reports (any user) ------------------------------------------------------

type reportRequest struct {
	Reason  entity.ReportReason `json:"reason"`
	Comment string              `json:"comment"`
}

func (s *Server) handleCreateReport(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "media not found")
		return
	}

	var req reportRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if !entity.ValidReportReason(req.Reason) {
		writeError(w, http.StatusBadRequest, "reason must be one of copyright, illegal, nsfw, other")
		return
	}
	if len(req.Comment) > 500 {
		writeError(w, http.StatusBadRequest, "comment must be at most 500 characters")
		return
	}

	if _, err := s.deps.Admin.GetMedia(r.Context(), id); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusNotFound, "media not found")
			return
		}
		s.internalError(w, r, "load media", err)
		return
	}

	err = s.deps.Admin.CreateReport(r.Context(), id, user.ID, req.Reason, strings.TrimSpace(req.Comment))
	switch {
	case errors.Is(err, repository.ErrConflict):
		writeError(w, http.StatusConflict, "you have already reported this video")
		return
	case err != nil:
		s.internalError(w, r, "create report", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- admin -------------------------------------------------------------------

type statsResponse struct {
	Users         int            `json:"users"`
	BannedUsers   int            `json:"bannedUsers"`
	Rooms         int            `json:"rooms"`
	PrivateRooms  int            `json:"privateRooms"`
	MediaByStatus map[string]int `json:"mediaByStatus"`
	MediaBytes    int64          `json:"mediaBytes"`
	PendingJobs   int            `json:"pendingJobs"`
	RunningJobs   int            `json:"runningJobs"`
	FailedJobs    int            `json:"failedJobs"`
	OpenReports   int            `json:"openReports"`
	RoomsLoaded   int            `json:"roomsLoaded"`
}

func (s *Server) handleAdminStats(w http.ResponseWriter, r *http.Request) {
	st, err := s.deps.Admin.Stats(r.Context())
	if err != nil {
		s.internalError(w, r, "stats", err)
		return
	}
	resp := statsResponse{
		Users: st.Users, BannedUsers: st.BannedUsers, Rooms: st.Rooms, PrivateRooms: st.PrivateRooms,
		MediaByStatus: st.MediaByStatus, MediaBytes: st.MediaBytes, PendingJobs: st.PendingJobs,
		RunningJobs: st.RunningJobs, FailedJobs: st.FailedJobs, OpenReports: st.OpenReports,
	}
	if s.deps.RoomsLoaded != nil {
		resp.RoomsLoaded = s.deps.RoomsLoaded()
	}
	writeJSON(w, http.StatusOK, resp)
}

type adminUserResponse struct {
	ID           uuid.UUID   `json:"id"`
	Username     string      `json:"username"`
	Role         entity.Role `json:"role"`
	Banned       bool        `json:"banned"`
	BannedReason string      `json:"bannedReason,omitempty"`
	RoomCount    int         `json:"roomCount"`
	CreatedAt    time.Time   `json:"createdAt"`
}

func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.deps.Admin.ListUsers(r.Context(), strings.TrimSpace(r.URL.Query().Get("q")), 100)
	if err != nil {
		s.internalError(w, r, "list users", err)
		return
	}
	out := make([]adminUserResponse, 0, len(users))
	for _, au := range users {
		u := au.User
		resp := adminUserResponse{ID: u.ID, Username: u.Username, Role: u.Role, Banned: u.IsBanned(), RoomCount: au.RoomCount, CreatedAt: u.CreatedAt}
		if u.BannedReason != nil {
			resp.BannedReason = *u.BannedReason
		}
		out = append(out, resp)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAdminBanUser(w http.ResponseWriter, r *http.Request) {
	admin := auth.UserFrom(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if id == admin.ID {
		writeError(w, http.StatusBadRequest, "you cannot ban yourself")
		return
	}
	target, err := s.deps.Rooms.GetUserByID(r.Context(), id)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		s.internalError(w, r, "load user", err)
		return
	}
	if target.IsAdmin() {
		writeError(w, http.StatusBadRequest, "administrators cannot be banned")
		return
	}

	var req banRequest
	if r.ContentLength != 0 && !decodeJSON(w, r, &req) {
		return
	}
	if err := s.deps.Admin.BanUser(r.Context(), id, admin.ID, req.Reason, time.Now()); err != nil {
		s.internalError(w, r, "ban user", err)
		return
	}
	if err := s.deps.Sessions.RevokeAll(r.Context(), id); err != nil {
		s.deps.Logger.ErrorContext(r.Context(), "revoke sessions", "error", err)
	}
	if s.deps.OnUserBanned != nil {
		s.deps.OnUserBanned(id)
	}
	s.audit(r, "user.ban", "user", id.String(), nil, map[string]any{"username": target.Username, "reason": req.Reason})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAdminUnbanUser(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	err = s.deps.Admin.UnbanUser(r.Context(), id)
	switch {
	case errors.Is(err, repository.ErrNotFound):
		writeError(w, http.StatusNotFound, "user not found")
		return
	case err != nil:
		s.internalError(w, r, "unban user", err)
		return
	}
	s.audit(r, "user.unban", "user", id.String(), nil, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAdminRooms(w http.ResponseWriter, r *http.Request) {
	rooms, err := s.deps.Admin.ListRooms(r.Context(), strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q"))), 100)
	if err != nil {
		s.internalError(w, r, "list rooms", err)
		return
	}
	ids := make([]uuid.UUID, 0, len(rooms))
	for _, rm := range rooms {
		ids = append(ids, rm.OwnerID)
	}
	names, err := s.deps.Admin.UsernamesByID(r.Context(), ids)
	if err != nil {
		s.internalError(w, r, "owner names", err)
		return
	}
	out := make([]roomResponse, 0, len(rooms))
	for _, rm := range rooms {
		out = append(out, roomResponse{ID: rm.ID, Slug: rm.Slug, Name: rm.Name, Visibility: rm.Visibility, Settings: rm.Settings, Owner: names[rm.OwnerID], CreatedAt: rm.CreatedAt})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAdminDeleteRoom(w http.ResponseWriter, r *http.Request) {
	rm, err := s.deps.Admin.GetRoomBySlug(r.Context(), chi.URLParam(r, "slug"))
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, "room not found")
		return
	}
	if err != nil {
		s.internalError(w, r, "load room", err)
		return
	}
	if err := s.deps.Admin.DeleteRoom(r.Context(), rm.ID); err != nil {
		s.internalError(w, r, "delete room", err)
		return
	}
	if s.deps.OnRoomDeleted != nil {
		s.deps.OnRoomDeleted(rm.ID)
	}
	s.audit(r, "room.delete", "room", rm.ID.String(), nil, map[string]any{"slug": rm.Slug, "name": rm.Name, "admin": true})
	w.WriteHeader(http.StatusNoContent)
}

type reportResponse struct {
	ID        uuid.UUID           `json:"id"`
	Reporter  string              `json:"reporter"`
	Reason    entity.ReportReason `json:"reason"`
	Comment   string              `json:"comment"`
	CreatedAt time.Time           `json:"createdAt"`
}

type reportedMediaResponse struct {
	Media   adminMediaResponse `json:"media"`
	Count   int                `json:"count"`
	Reports []reportResponse   `json:"reports"`
}

type adminMediaResponse struct {
	ID        uuid.UUID          `json:"id"`
	SourceKey string             `json:"sourceKey"`
	SourceURL string             `json:"sourceUrl"`
	Title     string             `json:"title"`
	Status    entity.MediaStatus `json:"status"`
	SizeBytes int64              `json:"sizeBytes"`
	Thumbnail string             `json:"thumbnailUrl"`
}

func (s *Server) handleAdminReports(w http.ResponseWriter, r *http.Request) {
	list, err := s.deps.Admin.ListReportedMedia(r.Context(), 100)
	if err != nil {
		s.internalError(w, r, "list reports", err)
		return
	}
	out := make([]reportedMediaResponse, 0, len(list))
	for _, rm := range list {
		m := rm.Media
		resp := reportedMediaResponse{
			Media:   adminMediaResponse{ID: m.ID, SourceKey: m.SourceKey, SourceURL: m.SourceURL, Title: m.Title, Status: m.Status, SizeBytes: m.SizeBytes, Thumbnail: m.ThumbnailURL},
			Count:   rm.Count,
			Reports: make([]reportResponse, 0, len(rm.Reports)),
		}
		for _, rep := range rm.Reports {
			resp.Reports = append(resp.Reports, reportResponse{ID: rep.ID, Reporter: rep.Reporter, Reason: rep.Reason, Comment: rep.Comment, CreatedAt: rep.CreatedAt})
		}
		out = append(out, resp)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleAdminDismissReports closes every open report on a media item.
func (s *Server) handleAdminDismissReports(w http.ResponseWriter, r *http.Request) {
	admin := auth.UserFrom(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "media not found")
		return
	}
	n, err := s.deps.Admin.ResolveReports(r.Context(), id, admin.ID)
	if err != nil {
		s.internalError(w, r, "resolve reports", err)
		return
	}
	s.audit(r, "reports.dismiss", "media", id.String(), nil, map[string]any{"count": n})
	w.WriteHeader(http.StatusNoContent)
}

// handleAdminDeleteMedia removes a media item everywhere: objects in
// storage, the row (queue items cascade), open reports, and blocks the
// source so it cannot be re-added.
func (s *Server) handleAdminDeleteMedia(w http.ResponseWriter, r *http.Request) {
	admin := auth.UserFrom(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "media not found")
		return
	}
	media, err := s.deps.Admin.GetMedia(r.Context(), id)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, "media not found")
		return
	}
	if err != nil {
		s.internalError(w, r, "load media", err)
		return
	}

	var req banRequest
	if r.ContentLength != 0 && !decodeJSON(w, r, &req) {
		return
	}

	if err := s.deps.Admin.BlockSource(r.Context(), media.SourceKey, req.Reason, &admin.ID); err != nil {
		s.internalError(w, r, "block source", err)
		return
	}
	if s.deps.MediaObjects != nil && media.S3Prefix != "" {
		if err := s.deps.MediaObjects.DeletePrefix(r.Context(), media.S3Prefix); err != nil {
			s.deps.Logger.ErrorContext(r.Context(), "delete media objects", "media", id, "error", err)
		}
	}
	if _, err := s.deps.Admin.ResolveReports(r.Context(), id, admin.ID); err != nil {
		s.deps.Logger.ErrorContext(r.Context(), "resolve reports", "error", err)
	}
	if err := s.deps.Admin.DeleteMedia(r.Context(), id); err != nil {
		s.internalError(w, r, "delete media", err)
		return
	}
	if s.deps.OnMediaDeleted != nil {
		s.deps.OnMediaDeleted(id)
	}
	s.audit(r, "media.delete", "media", id.String(), nil, map[string]any{"sourceKey": media.SourceKey, "title": media.Title, "reason": req.Reason})
	w.WriteHeader(http.StatusNoContent)
}

type blocklistResponse struct {
	SourceKey string    `json:"sourceKey"`
	Reason    string    `json:"reason"`
	CreatedBy string    `json:"createdBy"`
	CreatedAt time.Time `json:"createdAt"`
}

func (s *Server) handleAdminBlocklist(w http.ResponseWriter, r *http.Request) {
	list, err := s.deps.Admin.ListBlocklist(r.Context())
	if err != nil {
		s.internalError(w, r, "list blocklist", err)
		return
	}
	out := make([]blocklistResponse, 0, len(list))
	for _, e := range list {
		out = append(out, blocklistResponse{SourceKey: e.SourceKey, Reason: e.Reason, CreatedBy: e.CreatedBy, CreatedAt: e.CreatedAt})
	}
	writeJSON(w, http.StatusOK, out)
}

type blockRequest struct {
	SourceKey string `json:"sourceKey"`
	Reason    string `json:"reason"`
}

func (s *Server) handleAdminBlock(w http.ResponseWriter, r *http.Request) {
	admin := auth.UserFrom(r.Context())
	var req blockRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.SourceKey = strings.TrimSpace(req.SourceKey)
	if req.SourceKey == "" || len(req.SourceKey) > 500 {
		writeError(w, http.StatusBadRequest, "sourceKey is required")
		return
	}
	if err := s.deps.Admin.BlockSource(r.Context(), req.SourceKey, req.Reason, &admin.ID); err != nil {
		s.internalError(w, r, "block source", err)
		return
	}
	s.audit(r, "source.block", "source", req.SourceKey, nil, map[string]any{"reason": req.Reason})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAdminUnblock(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "*")
	err := s.deps.Admin.UnblockSource(r.Context(), key)
	switch {
	case errors.Is(err, repository.ErrNotFound):
		writeError(w, http.StatusNotFound, "source is not blocked")
		return
	case err != nil:
		s.internalError(w, r, "unblock source", err)
		return
	}
	s.audit(r, "source.unblock", "source", key, nil, nil)
	w.WriteHeader(http.StatusNoContent)
}

type auditResponse struct {
	ID         int64          `json:"id"`
	Actor      string         `json:"actor"`
	Action     string         `json:"action"`
	TargetType string         `json:"targetType"`
	TargetID   string         `json:"targetId"`
	RoomID     *uuid.UUID     `json:"roomId"`
	Meta       map[string]any `json:"meta"`
	CreatedAt  time.Time      `json:"createdAt"`
}

func (s *Server) handleAdminAudit(w http.ResponseWriter, r *http.Request) {
	var roomID *uuid.UUID
	if v := r.URL.Query().Get("room"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "room must be a uuid")
			return
		}
		roomID = &id
	}
	entries, err := s.deps.Admin.ListAudit(r.Context(), roomID, 200)
	if err != nil {
		s.internalError(w, r, "list audit", err)
		return
	}
	ids := make([]uuid.UUID, 0, len(entries))
	for _, e := range entries {
		if e.ActorID != nil {
			ids = append(ids, *e.ActorID)
		}
	}
	names, err := s.deps.Admin.UsernamesByID(r.Context(), ids)
	if err != nil {
		s.internalError(w, r, "actor names", err)
		return
	}
	out := make([]auditResponse, 0, len(entries))
	for _, e := range entries {
		resp := auditResponse{ID: e.ID, Action: e.Action, TargetType: e.TargetType, TargetID: e.TargetID, RoomID: e.RoomID, Meta: e.Meta, CreatedAt: e.CreatedAt}
		if e.ActorID != nil {
			resp.Actor = names[*e.ActorID]
		}
		if resp.Meta == nil {
			resp.Meta = map[string]any{}
		}
		out = append(out, resp)
	}
	writeJSON(w, http.StatusOK, out)
}
