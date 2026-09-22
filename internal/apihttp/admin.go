package apihttp

import (
	"context"
	"errors"
	"net/http"
	"strconv"
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
	CreateReport(ctx context.Context, mediaID, reporterID uuid.UUID, roomID, addedBy *uuid.UUID, reason entity.ReportReason, comment string) error
	QueueAdderInRoom(ctx context.Context, roomID, mediaID uuid.UUID) (*uuid.UUID, bool, error)
	ListReportedMedia(ctx context.Context, limit int) ([]entity.ReportedMedia, error)
	ResolveReports(ctx context.Context, mediaID, resolvedBy uuid.UUID) (int64, error)
	DeleteMedia(ctx context.Context, id uuid.UUID) error
	BlockSource(ctx context.Context, sourceKey, reason string, createdBy *uuid.UUID) error
	IsSourceBlocked(ctx context.Context, sourceKey string) (bool, error)
	ListBlocklist(ctx context.Context) ([]entity.BlocklistEntry, error)
	UnblockSource(ctx context.Context, sourceKey string) error
	Stats(ctx context.Context) (*entity.Stats, error)
	ListUsers(ctx context.Context, query string, limit int) ([]entity.AdminUser, error)
	ListRooms(ctx context.Context, query string, limit int) ([]entity.Room, error)
	ListAudit(ctx context.Context, q repository.AuditQuery) ([]entity.AuditEntry, error)
	ListMediaBySize(ctx context.Context, limit int) ([]repository.StorageItem, error)
	SumReadyMediaBytes(ctx context.Context) (int64, error)
	ListEvictableMedia(ctx context.Context, limit int) ([]entity.Media, error)
	MediaQueued(ctx context.Context, mediaID uuid.UUID) (bool, error)
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
	Reason   entity.ReportReason `json:"reason"`
	Comment  string              `json:"comment"`
	RoomSlug string              `json:"roomSlug"` // where the reporter saw it; optional
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
	if !allowUser(w, s.deps.ReportLimiter, user.ID) {
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

	// Capture the room and who queued the video there, when the slug is
	// known and the media is actually in that queue.
	var roomID, addedBy *uuid.UUID
	if slug := strings.TrimSpace(req.RoomSlug); slug != "" {
		if room, err := s.deps.Admin.GetRoomBySlug(r.Context(), slug); err == nil {
			if adder, queued, err := s.deps.Admin.QueueAdderInRoom(r.Context(), room.ID, id); err == nil && queued {
				roomID, addedBy = &room.ID, adder
			}
		}
	}

	err = s.deps.Admin.CreateReport(r.Context(), id, user.ID, roomID, addedBy, req.Reason, strings.TrimSpace(req.Comment))
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

// evictMedia drops the packaged objects and the row without touching the
// blocklist; the source can be queued again and will be ingested afresh.
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
	RoomSlug  string              `json:"roomSlug,omitempty"`
	RoomName  string              `json:"roomName,omitempty"`
	AddedBy   string              `json:"addedBy,omitempty"`
	Reason    entity.ReportReason `json:"reason"`
	Comment   string              `json:"comment"`
	CreatedAt time.Time           `json:"createdAt"`
}

type placementResponse struct {
	RoomSlug string `json:"roomSlug"`
	RoomName string `json:"roomName"`
	AddedBy  string `json:"addedBy"`
}

type reportedMediaResponse struct {
	Media      adminMediaResponse  `json:"media"`
	Count      int                 `json:"count"`
	Reports    []reportResponse    `json:"reports"`
	Placements []placementResponse `json:"placements"`
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
			Media:      adminMediaResponse{ID: m.ID, SourceKey: m.SourceKey, SourceURL: m.SourceURL, Title: m.Title, Status: m.Status, SizeBytes: m.SizeBytes, Thumbnail: m.ThumbnailURL},
			Count:      rm.Count,
			Reports:    make([]reportResponse, 0, len(rm.Reports)),
			Placements: make([]placementResponse, 0, len(rm.Placements)),
		}
		for _, rep := range rm.Reports {
			resp.Reports = append(resp.Reports, reportResponse{
				ID: rep.ID, Reporter: rep.Reporter, RoomSlug: rep.RoomSlug, RoomName: rep.RoomName, AddedBy: rep.AddedBy,
				Reason: rep.Reason, Comment: rep.Comment, CreatedAt: rep.CreatedAt,
			})
		}
		for _, p := range rm.Placements {
			resp.Placements = append(resp.Placements, placementResponse{RoomSlug: p.RoomSlug, RoomName: p.RoomName, AddedBy: p.AddedBy})
		}
		out = append(out, resp)
	}
	writeJSON(w, http.StatusOK, out)
}

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

// auditPage is one page of the log; NextBefore pages further back (0 at
// the end).
type auditPage struct {
	Entries    []auditResponse `json:"entries"`
	NextBefore int64           `json:"nextBefore"`
}

const auditPageSize = 100

// handleAdminAudit lists the log newest first: ?room=<uuid>,
// ?actor=<username>, ?action=<prefix>, ?before=<id> page backwards.
func (s *Server) handleAdminAudit(w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()
	q := repository.AuditQuery{Action: strings.TrimSpace(qs.Get("action")), Limit: auditPageSize + 1}
	if v := qs.Get("room"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "room must be a uuid")
			return
		}
		q.RoomID = &id
	}
	if v := strings.TrimSpace(qs.Get("actor")); v != "" {
		u, err := s.deps.Users.GetUserByUsername(r.Context(), v)
		if errors.Is(err, repository.ErrNotFound) {
			writeJSON(w, http.StatusOK, auditPage{Entries: []auditResponse{}})
			return
		}
		if err != nil {
			s.internalError(w, r, "resolve actor", err)
			return
		}
		q.ActorID = &u.ID
	}
	if v := qs.Get("before"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id < 0 {
			writeError(w, http.StatusBadRequest, "before must be an id")
			return
		}
		q.BeforeID = id
	}
	entries, err := s.deps.Admin.ListAudit(r.Context(), q)
	if err != nil {
		s.internalError(w, r, "list audit", err)
		return
	}
	var nextBefore int64
	if len(entries) > auditPageSize {
		entries = entries[:auditPageSize]
		nextBefore = entries[len(entries)-1].ID
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
	writeJSON(w, http.StatusOK, auditPage{Entries: out, NextBefore: nextBefore})
}

// --- storage -------------------------------------------------------------------

type storageMedia struct {
	ID             uuid.UUID `json:"id"`
	Title          string    `json:"title"`
	SourceURL      string    `json:"sourceUrl"`
	SizeBytes      int64     `json:"sizeBytes"`
	Queued         bool      `json:"queued"`
	LastAccessedAt time.Time `json:"lastAccessedAt"`
	CreatedAt      time.Time `json:"createdAt"`
}

type storageResponse struct {
	TotalBytes  int64          `json:"totalBytes"`
	BudgetBytes int64          `json:"budgetBytes"`
	Media       []storageMedia `json:"media"`
}

// handleAdminStorage reports what the packaged media takes and the
// largest items; queued ones cannot be evicted.
func (s *Server) handleAdminStorage(w http.ResponseWriter, r *http.Request) {
	total, err := s.deps.Admin.SumReadyMediaBytes(r.Context())
	if err != nil {
		s.internalError(w, r, "sum media", err)
		return
	}
	items, err := s.deps.Admin.ListMediaBySize(r.Context(), 100)
	if err != nil {
		s.internalError(w, r, "list media", err)
		return
	}
	resp := storageResponse{TotalBytes: total, BudgetBytes: s.deps.CacheBudget, Media: make([]storageMedia, 0, len(items))}
	for _, it := range items {
		m := it.Media
		resp.Media = append(resp.Media, storageMedia{
			ID: m.ID, Title: m.Title, SourceURL: m.SourceURL, SizeBytes: m.SizeBytes, Queued: it.Queued,
			LastAccessedAt: m.LastAccessedAt, CreatedAt: m.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// evictMedia drops the packaged objects and the row without touching the
// blocklist; the source can be queued again and will be ingested afresh.
func (s *Server) evictMedia(ctx context.Context, m *entity.Media) error {
	if s.deps.MediaObjects != nil && m.S3Prefix != "" {
		if err := s.deps.MediaObjects.DeletePrefix(ctx, m.S3Prefix); err != nil {
			return err
		}
	}
	if err := s.deps.Admin.DeleteMedia(ctx, m.ID); err != nil {
		return err
	}
	if s.deps.OnMediaDeleted != nil {
		s.deps.OnMediaDeleted(m.ID)
	}
	return nil
}

// handleAdminEvictMedia evicts one item (POST /admin/media/{id}/evict);
// a queued item is refused.
func (s *Server) handleAdminEvictMedia(w http.ResponseWriter, r *http.Request) {
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
	queued, err := s.deps.Admin.MediaQueued(r.Context(), id)
	if err != nil {
		s.internalError(w, r, "check queues", err)
		return
	}
	if queued {
		writeError(w, http.StatusConflict, "this video is still queued in a room")
		return
	}
	if err := s.evictMedia(r.Context(), media); err != nil {
		s.internalError(w, r, "evict media", err)
		return
	}
	s.audit(r, "media.evict", "media", id.String(), nil, map[string]any{"title": media.Title, "bytes": media.SizeBytes, "by": admin.Username})
	w.WriteHeader(http.StatusNoContent)
}

type evictRequest struct {
	OlderThanDays int `json:"olderThanDays"`
}

type evictResponse struct {
	Removed int   `json:"removed"`
	Bytes   int64 `json:"bytes"`
}

// handleAdminEvictStale evicts every unqueued item not watched for the
// given number of days (POST /admin/storage/evict {olderThanDays}).
func (s *Server) handleAdminEvictStale(w http.ResponseWriter, r *http.Request) {
	var req evictRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.OlderThanDays < 1 || req.OlderThanDays > 3650 {
		writeError(w, http.StatusBadRequest, "olderThanDays must be between 1 and 3650")
		return
	}
	cutoff := time.Now().Add(-time.Duration(req.OlderThanDays) * 24 * time.Hour)
	candidates, err := s.deps.Admin.ListEvictableMedia(r.Context(), 500)
	if err != nil {
		s.internalError(w, r, "list evictable", err)
		return
	}
	var resp evictResponse
	for i := range candidates {
		m := &candidates[i]
		if !m.LastAccessedAt.Before(cutoff) {
			break // least recently accessed first
		}
		if err := s.evictMedia(r.Context(), m); err != nil {
			s.deps.Logger.ErrorContext(r.Context(), "evict media", "media", m.ID, "error", err)
			continue
		}
		resp.Removed++
		resp.Bytes += m.SizeBytes
	}
	s.audit(r, "storage.evict", "storage", "stale", nil, map[string]any{"olderThanDays": req.OlderThanDays, "removed": resp.Removed, "bytes": resp.Bytes})
	writeJSON(w, http.StatusOK, resp)
}
