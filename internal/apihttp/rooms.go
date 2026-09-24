package apihttp

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"uuid"

	"github.com/go-chi/chi/v5"

	"github.com/anesthetised/couchcast/internal/access"
	"github.com/anesthetised/couchcast/internal/auth"
	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/repository"
)

// RoomStore is what the room handlers need from the repository.
type RoomStore interface {
	CreateRoom(ctx context.Context, slug, name string, ownerID uuid.UUID, visibility entity.Visibility, settings entity.Settings) (*entity.Room, error)
	GetRoomBySlug(ctx context.Context, slug string) (*entity.Room, error)
	UpdateRoom(ctx context.Context, id uuid.UUID, slug, name string, visibility entity.Visibility, description string, scheduledAt *time.Time) (*entity.Room, error)
	ListUpcomingForUser(ctx context.Context, userID uuid.UUID, now, until time.Time) ([]repository.UpcomingRoom, error)
	TransferOwnership(ctx context.Context, roomID, from, to uuid.UUID) error
	DeleteRoom(ctx context.Context, id uuid.UUID) error
	ListRoomsForUser(ctx context.Context, userID uuid.UUID) ([]repository.RoomWithRole, error)
	CountMembers(ctx context.Context, roomID uuid.UUID) (int, error)

	GetMember(ctx context.Context, roomID, userID uuid.UUID) (*entity.RoomMember, error)
	ListMembers(ctx context.Context, roomID uuid.UUID) ([]entity.RoomMember, error)
	UpsertMember(ctx context.Context, roomID, userID uuid.UUID, role entity.RoomRole) error
	DeleteMember(ctx context.Context, roomID, userID uuid.UUID) error

	GetBan(ctx context.Context, roomID, userID uuid.UUID) (*entity.RoomBan, error)
	ListBans(ctx context.Context, roomID uuid.UUID) ([]entity.RoomBan, error)
	CreateBan(ctx context.Context, roomID, userID, bannedBy uuid.UUID, reason string) error
	DeleteBan(ctx context.Context, roomID, userID uuid.UUID) error

	CreateInvite(ctx context.Context, roomID, inviteeID, inviterID uuid.UUID) (*entity.Invite, error)
	GetInvite(ctx context.Context, id uuid.UUID) (*entity.Invite, error)
	ListPendingInvites(ctx context.Context, inviteeID uuid.UUID) ([]entity.Invite, error)
	AcceptInvite(ctx context.Context, id uuid.UUID) error
	DeclineInvite(ctx context.Context, id uuid.UUID) error

	GetUserByID(ctx context.Context, id uuid.UUID) (*entity.User, error)
	RecordAudit(ctx context.Context, e entity.AuditEntry) error
}

var slugRe = regexp.MustCompile(`^[a-z0-9-]{3,32}$`)

// reservedSlugs are top-level paths of the SPA and API that a room slug
// must never shadow.
var reservedSlugs = map[string]bool{
	"api": true, "media": true, "admin": true, "login": true, "register": true, "logout": true,
	"settings": true, "invites": true, "join": true, "metrics": true, "healthz": true, "assets": true, "r": true,
	"rooms": true, "me": true, "new": true, "about": true, "help": true, "static": true, "public": true,
}

const (
	slugAlphabet   = "abcdefghijklmnopqrstuvwxyz234567" // base32, lower-case
	slugLength     = 8
	maxRoomName    = 80
	maxDescription = 300
)

// generateSlug returns a random 8-character base32 slug.
func generateSlug() string {
	b := make([]byte, slugLength)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = slugAlphabet[int(b[i])%len(slugAlphabet)]
	}
	return string(b)
}

// validateSlug lower-cases and checks a user-supplied slug, returning a
// message on failure.
func validateSlug(slug string) (string, string) {
	slug = strings.ToLower(strings.TrimSpace(slug))
	if !slugRe.MatchString(slug) {
		return "", "slug must be 3-32 characters: lowercase letters, digits and hyphens"
	}
	if reservedSlugs[slug] {
		return "", "this slug is reserved"
	}
	return slug, ""
}

// validateDescription trims and bounds the room description.
func validateDescription(d string) (string, string) {
	d = strings.TrimSpace(d)
	if len(d) > maxDescription {
		return "", "description must be at most 300 characters"
	}
	return d, ""
}

func validateRoomName(name string) (string, string) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxRoomName {
		return "", "name must be between 1 and 80 characters"
	}
	return name, ""
}

func parseVisibility(v string) (entity.Visibility, bool) {
	switch entity.Visibility(v) {
	case entity.VisibilityPublic, "":
		return entity.VisibilityPublic, true
	case entity.VisibilityPrivate:
		return entity.VisibilityPrivate, true
	default:
		return "", false
	}
}

// --- responses ---------------------------------------------------------------

type roomResponse struct {
	ID          uuid.UUID         `json:"id"`
	Slug        string            `json:"slug"`
	Name        string            `json:"name"`
	Visibility  entity.Visibility `json:"visibility"`
	Settings    entity.Settings   `json:"settings"`
	Owner       string            `json:"owner"`
	Description string            `json:"description"`
	MemberCount int               `json:"memberCount"`
	MyRole      entity.RoomRole   `json:"myRole,omitempty"`
	Starred     bool              `json:"starred"`
	ScheduledAt *time.Time        `json:"scheduledAt"`
	CreatedAt   time.Time         `json:"createdAt"`
}

type memberResponse struct {
	Username string          `json:"username"`
	Role     entity.RoomRole `json:"role"`
	JoinedAt time.Time       `json:"joinedAt"`
}

type banResponse struct {
	Username  string    `json:"username"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"createdAt"`
}

type inviteResponse struct {
	ID        uuid.UUID           `json:"id"`
	RoomSlug  string              `json:"roomSlug"`
	RoomName  string              `json:"roomName"`
	Inviter   string              `json:"inviter"`
	Status    entity.InviteStatus `json:"status"`
	CreatedAt time.Time           `json:"createdAt"`
}

func toInviteResponse(i *entity.Invite) inviteResponse {
	return inviteResponse{ID: i.ID, RoomSlug: i.RoomSlug, RoomName: i.RoomName, Inviter: i.Inviter, Status: i.Status, CreatedAt: i.CreatedAt}
}

// --- request context ---------------------------------------------------------

// roomCtx is the room plus the requesting actor, resolved once per request.
type roomCtx struct {
	room  *entity.Room
	actor access.Actor
}

// loadRoom resolves {slug} and builds the actor. It writes 404 when the
// room does not exist and returns false.
func (s *Server) loadRoom(w http.ResponseWriter, r *http.Request) (roomCtx, bool) {
	ctx := r.Context()
	room, err := s.deps.Rooms.GetRoomBySlug(ctx, chi.URLParam(r, "slug"))
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, "room not found")
		return roomCtx{}, false
	}
	if err != nil {
		s.internalError(w, r, "load room", err)
		return roomCtx{}, false
	}

	actor, err := s.actorFor(ctx, room, auth.UserFrom(ctx))
	if err != nil {
		s.internalError(w, r, "load membership", err)
		return roomCtx{}, false
	}

	return roomCtx{room: room, actor: actor}, true
}

// actorFor loads membership and ban state for a user in a room.
func (s *Server) actorFor(ctx context.Context, room *entity.Room, user *entity.User) (access.Actor, error) {
	actor := access.Actor{User: user}
	if user == nil {
		return actor, nil
	}

	member, err := s.deps.Rooms.GetMember(ctx, room.ID, user.ID)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return actor, err
	}
	actor.Member = member

	_, err = s.deps.Rooms.GetBan(ctx, room.ID, user.ID)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return actor, err
	}
	actor.Banned = err == nil

	if s.deps.Mutes != nil {
		mute, err := s.deps.Mutes.GetMute(ctx, room.ID, user.ID)
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			return actor, err
		}
		if mute != nil && mute.Active(time.Now()) {
			actor.MutedUntil = &mute.Until
		}
	}

	return actor, nil
}

// require checks a permission and writes the matching error when denied.
func (s *Server) require(w http.ResponseWriter, rc roomCtx, action access.Action) bool {
	if access.Can(rc.actor, action, rc.room) {
		return true
	}
	s.denied(w, rc)
	return false
}

func (s *Server) denied(w http.ResponseWriter, rc roomCtx) {
	switch {
	case rc.actor.User == nil:
		writeError(w, http.StatusUnauthorized, "authentication required")
	case rc.actor.Banned:
		writeError(w, http.StatusForbidden, "you are banned from this room")
	case !rc.room.IsPublic() && rc.actor.Member == nil && !rc.actor.User.IsAdmin():
		writeError(w, http.StatusForbidden, "this room is private")
	default:
		writeError(w, http.StatusForbidden, "insufficient permissions")
	}
}

// targetUser resolves {username} to a user, writing 404 when missing.
func (s *Server) targetUser(w http.ResponseWriter, r *http.Request) (*entity.User, bool) {
	u, err := s.deps.Users.GetUserByUsername(r.Context(), chi.URLParam(r, "username"))
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, "user not found")
		return nil, false
	}
	if err != nil {
		s.internalError(w, r, "lookup user", err)
		return nil, false
	}
	return u, true
}

// targetRole returns the target's role in the room ("" for non-members).
func (s *Server) targetRole(ctx context.Context, room *entity.Room, userID uuid.UUID) (entity.RoomRole, error) {
	m, err := s.deps.Rooms.GetMember(ctx, room.ID, userID)
	if errors.Is(err, repository.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return m.Role, nil
}

// audit records a moderator action; failures are logged, never surfaced.
func (s *Server) audit(r *http.Request, action, targetType, targetID string, room *entity.Room, meta map[string]any) {
	e := entity.AuditEntry{Action: action, TargetType: targetType, TargetID: targetID, Meta: meta}
	if u := auth.UserFrom(r.Context()); u != nil {
		e.ActorID = &u.ID
	}
	if room != nil {
		e.RoomID = &room.ID
	}
	if err := s.deps.Rooms.RecordAudit(r.Context(), e); err != nil {
		s.deps.Logger.ErrorContext(r.Context(), "record audit", "error", err, "action", action)
	}
}

func (s *Server) roomResponse(ctx context.Context, rc roomCtx) (roomResponse, error) {
	owner, err := s.deps.Rooms.GetUserByID(ctx, rc.room.OwnerID)
	if err != nil {
		return roomResponse{}, err
	}
	count, err := s.deps.Rooms.CountMembers(ctx, rc.room.ID)
	if err != nil {
		return roomResponse{}, err
	}
	starred := false
	if s.deps.Stars != nil && rc.actor.User != nil {
		if starred, err = s.deps.Stars.IsStarred(ctx, rc.actor.User.ID, rc.room.ID); err != nil {
			return roomResponse{}, err
		}
	}
	return roomResponse{
		ID: rc.room.ID, Slug: rc.room.Slug, Name: rc.room.Name, Visibility: rc.room.Visibility,
		Settings: rc.room.Settings, Owner: owner.Username, Description: rc.room.Description, MemberCount: count, MyRole: rc.actor.Role(),
		Starred: starred, ScheduledAt: rc.room.ScheduledAt, CreatedAt: rc.room.CreatedAt,
	}, nil
}

// ActorFor exposes actor resolution to the WebSocket hub.
func (s *Server) ActorFor(ctx context.Context, room *entity.Room, user *entity.User) (access.Actor, error) {
	return s.actorFor(ctx, room, user)
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.loadRoom(w, r)
	if !ok || !s.require(w, rc, access.ViewRoom) {
		return
	}
	s.deps.WS.Serve(w, r, rc.room, rc.actor)
}

// --- rooms -------------------------------------------------------------------

type createRoomRequest struct {
	Name        string   `json:"name"`
	Slug        string   `json:"slug"`
	Visibility  string   `json:"visibility"`
	Description string   `json:"description"`
	ScheduledAt *string  `json:"scheduledAt"`
	FirstURL    string   `json:"firstUrl"`
	Invites     []string `json:"invites"`
	Settings    *struct {
		VoteMode      *bool `json:"voteMode"`
		ViewersCanAdd *bool `json:"viewersCanAdd"`
	} `json:"settings"`
}

// createRoomResponse is the room plus the outcome of the optional extras.
type createRoomResponse struct {
	roomResponse
	Warnings []string `json:"warnings,omitempty"`
}

func (s *Server) handleCreateRoom(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())

	var req createRoomRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	name, msg := validateRoomName(req.Name)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	description, msg := validateDescription(req.Description)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	visibility, ok := parseVisibility(req.Visibility)
	if !ok {
		writeError(w, http.StatusBadRequest, "visibility must be public or private")
		return
	}
	var scheduledAt *time.Time
	if req.ScheduledAt != nil && *req.ScheduledAt != "" {
		if scheduledAt, msg = parseSchedule(*req.ScheduledAt); msg != "" {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
	}

	if !allowUser(w, s.deps.RoomCreateLimiter, user.ID) {
		return
	}

	custom := strings.TrimSpace(req.Slug) != ""
	slug := generateSlug()
	if custom {
		if slug, msg = validateSlug(req.Slug); msg != "" {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
	}

	settings := entity.DefaultSettings()
	if req.Settings != nil {
		if req.Settings.VoteMode != nil {
			settings.VoteMode = *req.Settings.VoteMode
		}
		if req.Settings.ViewersCanAdd != nil {
			settings.ViewersCanAdd = *req.Settings.ViewersCanAdd
		}
	}

	// Reject a bad first video before anything is created.
	firstURL := strings.TrimSpace(req.FirstURL)
	if firstURL != "" {
		if s.deps.Admit == nil || s.deps.LiveQueue == nil {
			writeError(w, http.StatusBadRequest, "starting with a video is not available")
			return
		}
		key, err := s.deps.Admit.Key(firstURL)
		if err != nil {
			writeError(w, http.StatusBadRequest, "the first video link is not supported")
			return
		}
		if blocked, err := s.deps.Admin.IsSourceBlocked(r.Context(), key); err != nil {
			s.internalError(w, r, "check blocklist", err)
			return
		} else if blocked {
			writeError(w, http.StatusBadRequest, "the first video has been blocked by an administrator")
			return
		}
	}
	if len(req.Invites) > 50 {
		writeError(w, http.StatusBadRequest, "at most 50 invites at creation")
		return
	}

	var room *entity.Room
	for attempt := 0; ; attempt++ {
		var err error
		room, err = s.deps.Rooms.CreateRoom(r.Context(), slug, name, user.ID, visibility, settings)
		if err == nil {
			break
		}
		if !errors.Is(err, repository.ErrConflict) {
			s.internalError(w, r, "create room", err)
			return
		}
		if custom || attempt >= 3 {
			writeError(w, http.StatusConflict, "slug is taken")
			return
		}
		slug = generateSlug()
	}

	if description != "" || scheduledAt != nil {
		if updated, err := s.deps.Rooms.UpdateRoom(r.Context(), room.ID, room.Slug, room.Name, room.Visibility, description, scheduledAt); err == nil {
			room = updated
		} else {
			s.internalError(w, r, "set description", err)
			return
		}
	}

	rc := roomCtx{room: room, actor: access.Actor{User: user, Member: &entity.RoomMember{Role: entity.RoomRoleOwner}}}
	var warnings []string

	// The room exists from here on: extras report problems as warnings.
	if firstURL != "" {
		if err := s.deps.LiveQueue.QueueAdd(r.Context(), room.ID, rc.actor, firstURL); err != nil {
			s.deps.Logger.WarnContext(r.Context(), "queue first video", "room", room.Slug, "error", err)
			warnings = append(warnings, "the first video could not be queued: "+err.Error())
		}
	}
	for _, name := range req.Invites {
		name = strings.TrimSpace(name)
		if name == "" || strings.EqualFold(name, user.Username) {
			continue
		}
		target, err := s.deps.Users.GetUserByUsername(r.Context(), name)
		if errors.Is(err, repository.ErrNotFound) {
			warnings = append(warnings, "no such user: "+name)
			continue
		}
		if err != nil {
			s.internalError(w, r, "lookup invitee", err)
			return
		}
		inv, err := s.deps.Rooms.CreateInvite(r.Context(), room.ID, target.ID, user.ID)
		if err != nil && !errors.Is(err, repository.ErrConflict) {
			s.internalError(w, r, "create invite", err)
			return
		}
		if inv != nil {
			s.notifyInvite(inv.ID, target.ID, user.Username, room)
		}
		s.audit(r, "invite.create", "user", target.ID.String(), room, map[string]any{"username": target.Username})
	}

	resp, err := s.roomResponse(r.Context(), rc)
	if err != nil {
		s.internalError(w, r, "room response", err)
		return
	}
	writeJSON(w, http.StatusCreated, createRoomResponse{roomResponse: resp, Warnings: warnings})
}

func (s *Server) handleGetRoom(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.loadRoom(w, r)
	if !ok || !s.require(w, rc, access.ViewRoom) {
		return
	}
	resp, err := s.roomResponse(r.Context(), rc)
	if err != nil {
		s.internalError(w, r, "room response", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

type updateRoomRequest struct {
	Name        *string `json:"name"`
	Slug        *string `json:"slug"`
	Visibility  *string `json:"visibility"`
	Description *string `json:"description"`
	// ScheduledAt is RFC 3339; an explicit null clears the announced start.
	ScheduledAt json.RawMessage `json:"scheduledAt"`
}

// parseSchedule reads an RFC 3339 start; it must be in the future.
func parseSchedule(raw string) (*time.Time, string) {
	at, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, "scheduledAt must be an RFC 3339 time"
	}
	if at.Before(time.Now()) {
		return nil, "the scheduled start must be in the future"
	}
	at = at.UTC()
	return &at, ""
}

func (s *Server) handleUpdateRoom(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.loadRoom(w, r)
	if !ok || !s.require(w, rc, access.ManageRoom) {
		return
	}

	var req updateRoomRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	name, slug, visibility, description := rc.room.Name, rc.room.Slug, rc.room.Visibility, rc.room.Description
	scheduledAt := rc.room.ScheduledAt
	var msg string
	if len(req.ScheduledAt) > 0 {
		if string(req.ScheduledAt) == "null" {
			scheduledAt = nil
		} else {
			var raw string
			if err := json.Unmarshal(req.ScheduledAt, &raw); err != nil {
				writeError(w, http.StatusBadRequest, "scheduledAt must be a string or null")
				return
			}
			if scheduledAt, msg = parseSchedule(raw); msg != "" {
				writeError(w, http.StatusBadRequest, msg)
				return
			}
		}
	}
	if req.Description != nil {
		if description, msg = validateDescription(*req.Description); msg != "" {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
	}
	if req.Name != nil {
		if name, msg = validateRoomName(*req.Name); msg != "" {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
	}
	if req.Slug != nil {
		if slug, msg = validateSlug(*req.Slug); msg != "" {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
	}
	if req.Visibility != nil {
		v, ok := parseVisibility(*req.Visibility)
		if !ok {
			writeError(w, http.StatusBadRequest, "visibility must be public or private")
			return
		}
		visibility = v
	}

	room, err := s.deps.Rooms.UpdateRoom(r.Context(), rc.room.ID, slug, name, visibility, description, scheduledAt)
	switch {
	case errors.Is(err, repository.ErrConflict):
		writeError(w, http.StatusConflict, "slug is taken")
		return
	case err != nil:
		s.internalError(w, r, "update room", err)
		return
	}

	s.audit(r, "room.update", "room", room.ID.String(), room, map[string]any{
		"name": name, "slug": slug, "visibility": visibility,
	})
	if s.deps.OnRoomChanged != nil {
		s.deps.OnRoomChanged(room.ID)
	}

	rc.room = room
	resp, err := s.roomResponse(r.Context(), rc)
	if err != nil {
		s.internalError(w, r, "room response", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleDeleteRoom(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.loadRoom(w, r)
	if !ok || !s.require(w, rc, access.DeleteRoom) {
		return
	}
	if err := s.deps.Rooms.DeleteRoom(r.Context(), rc.room.ID); err != nil {
		s.internalError(w, r, "delete room", err)
		return
	}
	s.audit(r, "room.delete", "room", rc.room.ID.String(), nil, map[string]any{"slug": rc.room.Slug, "name": rc.room.Name})
	if s.deps.OnRoomDeleted != nil {
		s.deps.OnRoomDeleted(rc.room.ID)
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleMyRooms lists the rooms the caller belongs to.
//
// Deprecated: superseded by GET /api/v1/rooms?mine=1 (the directory). Kept
// for compatibility; the SPA no longer calls it.
func (s *Server) handleMyRooms(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())
	rooms, err := s.deps.Rooms.ListRoomsForUser(r.Context(), user.ID)
	if err != nil {
		s.internalError(w, r, "list rooms", err)
		return
	}

	out := make([]roomResponse, 0, len(rooms))
	for i := range rooms {
		rm := &rooms[i].Room
		out = append(out, roomResponse{
			ID: rm.ID, Slug: rm.Slug, Name: rm.Name, Visibility: rm.Visibility, Settings: rm.Settings,
			MyRole: rooms[i].Role, CreatedAt: rm.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// --- members -----------------------------------------------------------------

func (s *Server) handleListMembers(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.loadRoom(w, r)
	if !ok || !s.require(w, rc, access.ViewRoom) {
		return
	}
	members, err := s.deps.Rooms.ListMembers(r.Context(), rc.room.ID)
	if err != nil {
		s.internalError(w, r, "list members", err)
		return
	}
	out := make([]memberResponse, 0, len(members))
	for _, m := range members {
		out = append(out, memberResponse{Username: m.Username, Role: m.Role, JoinedAt: m.JoinedAt})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAddModerator(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.loadRoom(w, r)
	if !ok || !s.require(w, rc, access.ManageModerators) {
		return
	}
	target, ok := s.targetUser(w, r)
	if !ok {
		return
	}
	if target.ID == rc.room.OwnerID {
		writeError(w, http.StatusBadRequest, "the owner is already above moderator")
		return
	}
	if _, err := s.deps.Rooms.GetBan(r.Context(), rc.room.ID, target.ID); err == nil {
		writeError(w, http.StatusConflict, "user is banned in this room; unban first")
		return
	}
	if err := s.deps.Rooms.UpsertMember(r.Context(), rc.room.ID, target.ID, entity.RoomRoleModerator); err != nil {
		s.internalError(w, r, "add moderator", err)
		return
	}
	s.audit(r, "member.promote", "user", target.ID.String(), rc.room, map[string]any{"username": target.Username})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRemoveModerator(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.loadRoom(w, r)
	if !ok || !s.require(w, rc, access.ManageModerators) {
		return
	}
	target, ok := s.targetUser(w, r)
	if !ok {
		return
	}
	role, err := s.targetRole(r.Context(), rc.room, target.ID)
	if err != nil {
		s.internalError(w, r, "target role", err)
		return
	}
	if role != entity.RoomRoleModerator {
		writeError(w, http.StatusNotFound, "user is not a moderator")
		return
	}
	if err := s.deps.Rooms.UpsertMember(r.Context(), rc.room.ID, target.ID, entity.RoomRoleMember); err != nil {
		s.internalError(w, r, "demote moderator", err)
		return
	}
	s.audit(r, "member.demote", "user", target.ID.String(), rc.room, map[string]any{"username": target.Username})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRemoveMember(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.loadRoom(w, r)
	if !ok {
		return
	}
	target, ok := s.targetUser(w, r)
	if !ok {
		return
	}
	role, err := s.targetRole(r.Context(), rc.room, target.ID)
	if err != nil {
		s.internalError(w, r, "target role", err)
		return
	}
	if role == "" {
		writeError(w, http.StatusNotFound, "user is not a member")
		return
	}
	// Anyone but the owner may leave on their own; the owner hands the
	// room over first.
	self := rc.actor.User != nil && rc.actor.User.ID == target.ID
	if self && role == entity.RoomRoleOwner {
		writeError(w, http.StatusConflict, "transfer ownership before leaving")
		return
	}
	if !self && !access.CanTarget(rc.actor, access.RemoveMember, rc.room, role) {
		s.denied(w, rc)
		return
	}
	if err := s.deps.Rooms.DeleteMember(r.Context(), rc.room.ID, target.ID); err != nil {
		s.internalError(w, r, "remove member", err)
		return
	}
	if self {
		s.audit(r, "member.leave", "room", rc.room.ID.String(), rc.room, map[string]any{"role": role})
		if s.deps.OnLeave != nil {
			s.deps.OnLeave(rc.room.ID, target.ID)
		}
	} else {
		s.audit(r, "member.remove", "user", target.ID.String(), rc.room, map[string]any{"username": target.Username, "role": role})
		if s.deps.OnBan != nil && !rc.room.IsPublic() {
			s.deps.OnBan(rc.room.ID, target.ID)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

type transferRequest struct {
	Username string `json:"username"`
}

// handleTransferOwnership hands the room to another member; the former
// owner stays as a moderator.
func (s *Server) handleTransferOwnership(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.loadRoom(w, r)
	if !ok || !s.require(w, rc, access.ManageRoom) {
		return
	}
	var req transferRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	target, err := s.deps.Users.GetUserByUsername(r.Context(), strings.TrimSpace(req.Username))
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		s.internalError(w, r, "lookup user", err)
		return
	}
	if target.ID == rc.actor.User.ID {
		writeError(w, http.StatusBadRequest, "you already own this room")
		return
	}
	err = s.deps.Rooms.TransferOwnership(r.Context(), rc.room.ID, rc.actor.User.ID, target.ID)
	switch {
	case errors.Is(err, repository.ErrNotFound):
		writeError(w, http.StatusNotFound, "user is not a member")
		return
	case err != nil:
		s.internalError(w, r, "transfer ownership", err)
		return
	}
	s.audit(r, "room.transfer", "user", target.ID.String(), rc.room, map[string]any{"username": target.Username})
	if s.deps.OnRoomChanged != nil {
		s.deps.OnRoomChanged(rc.room.ID)
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- bans --------------------------------------------------------------------

func (s *Server) handleListBans(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.loadRoom(w, r)
	if !ok || !s.require(w, rc, access.BanMember) {
		return
	}
	bans, err := s.deps.Rooms.ListBans(r.Context(), rc.room.ID)
	if err != nil {
		s.internalError(w, r, "list bans", err)
		return
	}
	out := make([]banResponse, 0, len(bans))
	for _, b := range bans {
		out = append(out, banResponse{Username: b.Username, Reason: b.Reason, CreatedAt: b.CreatedAt})
	}
	writeJSON(w, http.StatusOK, out)
}

type banRequest struct {
	Reason string `json:"reason"`
}

func (s *Server) handleBan(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.loadRoom(w, r)
	if !ok {
		return
	}
	target, ok := s.targetUser(w, r)
	if !ok {
		return
	}
	if target.ID == rc.actor.User.ID {
		writeError(w, http.StatusBadRequest, "you cannot ban yourself")
		return
	}

	role, err := s.targetRole(r.Context(), rc.room, target.ID)
	if err != nil {
		s.internalError(w, r, "target role", err)
		return
	}
	if !access.CanTarget(rc.actor, access.BanMember, rc.room, role) {
		s.denied(w, rc)
		return
	}

	var req banRequest
	if r.ContentLength != 0 && !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Reason) > 200 {
		writeError(w, http.StatusBadRequest, "reason must be at most 200 characters")
		return
	}

	if err := s.deps.Rooms.CreateBan(r.Context(), rc.room.ID, target.ID, rc.actor.User.ID, req.Reason); err != nil {
		s.internalError(w, r, "ban member", err)
		return
	}
	s.audit(r, "member.ban", "user", target.ID.String(), rc.room, map[string]any{"username": target.Username, "reason": req.Reason})
	if s.deps.OnBan != nil {
		s.deps.OnBan(rc.room.ID, target.ID)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUnban(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.loadRoom(w, r)
	if !ok || !s.require(w, rc, access.BanMember) {
		return
	}
	target, ok := s.targetUser(w, r)
	if !ok {
		return
	}
	err := s.deps.Rooms.DeleteBan(r.Context(), rc.room.ID, target.ID)
	switch {
	case errors.Is(err, repository.ErrNotFound):
		writeError(w, http.StatusNotFound, "user is not banned")
		return
	case err != nil:
		s.internalError(w, r, "unban member", err)
		return
	}
	s.audit(r, "member.unban", "user", target.ID.String(), rc.room, map[string]any{"username": target.Username})
	w.WriteHeader(http.StatusNoContent)
}

// --- invites -----------------------------------------------------------------

type inviteRequest struct {
	Username string `json:"username"`
}

func (s *Server) handleCreateInvite(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.loadRoom(w, r)
	if !ok || !s.require(w, rc, access.Invite) {
		return
	}

	var req inviteRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if !allowUser(w, s.deps.InviteLimiter, rc.actor.User.ID) {
		return
	}
	target, err := s.deps.Users.GetUserByUsername(r.Context(), strings.TrimSpace(req.Username))
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		s.internalError(w, r, "lookup user", err)
		return
	}

	if _, err := s.deps.Rooms.GetBan(r.Context(), rc.room.ID, target.ID); err == nil {
		writeError(w, http.StatusConflict, "user is banned in this room; unban first")
		return
	}
	if _, err := s.deps.Rooms.GetMember(r.Context(), rc.room.ID, target.ID); err == nil {
		writeError(w, http.StatusConflict, "user is already a member")
		return
	}

	inv, err := s.deps.Rooms.CreateInvite(r.Context(), rc.room.ID, target.ID, rc.actor.User.ID)
	switch {
	case errors.Is(err, repository.ErrConflict):
		writeError(w, http.StatusConflict, "an invite is already pending")
		return
	case err != nil:
		s.internalError(w, r, "create invite", err)
		return
	}

	s.audit(r, "invite.create", "user", target.ID.String(), rc.room, map[string]any{"username": target.Username})
	s.notifyInvite(inv.ID, target.ID, rc.actor.User.Username, rc.room)
	writeJSON(w, http.StatusCreated, toInviteResponse(inv))
}

func (s *Server) handleMyInvites(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())
	invites, err := s.deps.Rooms.ListPendingInvites(r.Context(), user.ID)
	if err != nil {
		s.internalError(w, r, "list invites", err)
		return
	}
	out := make([]inviteResponse, 0, len(invites))
	for i := range invites {
		out = append(out, toInviteResponse(&invites[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

// upcomingResponse is a member room with a start announced soon.
type upcomingResponse struct {
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	ScheduledAt time.Time `json:"scheduledAt"`
}

// upcomingHorizon is how far ahead /me/upcoming looks; the client warns
// ten minutes before, so a polling gap never misses a start.
const upcomingHorizon = 30 * time.Minute

func (s *Server) handleMyUpcoming(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())
	now := time.Now()
	rooms, err := s.deps.Rooms.ListUpcomingForUser(r.Context(), user.ID, now, now.Add(upcomingHorizon))
	if err != nil {
		s.internalError(w, r, "list upcoming", err)
		return
	}
	out := make([]upcomingResponse, 0, len(rooms))
	for _, u := range rooms {
		out = append(out, upcomingResponse{Slug: u.Slug, Name: u.Name, ScheduledAt: u.ScheduledAt})
	}
	writeJSON(w, http.StatusOK, out)
}

// loadOwnInvite resolves {id} and checks it belongs to the caller.
func (s *Server) loadOwnInvite(w http.ResponseWriter, r *http.Request) (*entity.Invite, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "invite not found")
		return nil, false
	}
	inv, err := s.deps.Rooms.GetInvite(r.Context(), id)
	if errors.Is(err, repository.ErrNotFound) || (err == nil && inv.InviteeID != auth.UserFrom(r.Context()).ID) {
		writeError(w, http.StatusNotFound, "invite not found")
		return nil, false
	}
	if err != nil {
		s.internalError(w, r, "load invite", err)
		return nil, false
	}
	return inv, true
}

func (s *Server) handleAcceptInvite(w http.ResponseWriter, r *http.Request) {
	inv, ok := s.loadOwnInvite(w, r)
	if !ok {
		return
	}
	if _, err := s.deps.Rooms.GetBan(r.Context(), inv.RoomID, inv.InviteeID); err == nil {
		writeError(w, http.StatusForbidden, "you are banned from this room")
		return
	}
	err := s.deps.Rooms.AcceptInvite(r.Context(), inv.ID)
	switch {
	case errors.Is(err, repository.ErrNotFound):
		writeError(w, http.StatusConflict, "invite is no longer pending")
		return
	case err != nil:
		s.internalError(w, r, "accept invite", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"roomSlug": inv.RoomSlug})
}

func (s *Server) handleDeclineInvite(w http.ResponseWriter, r *http.Request) {
	inv, ok := s.loadOwnInvite(w, r)
	if !ok {
		return
	}
	err := s.deps.Rooms.DeclineInvite(r.Context(), inv.ID)
	switch {
	case errors.Is(err, repository.ErrNotFound):
		writeError(w, http.StatusConflict, "invite is no longer pending")
		return
	case err != nil:
		s.internalError(w, r, "decline invite", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
