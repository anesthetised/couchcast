package apihttp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"time"

	"uuid"

	"github.com/go-chi/chi/v5"

	"github.com/anesthetised/couchcast/internal/access"
	"github.com/anesthetised/couchcast/internal/auth"
	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/repository"
)

// InviteLinkStore is what the invite-link handlers need.
type InviteLinkStore interface {
	CreateInviteLink(ctx context.Context, roomID uuid.UUID, tokenHash []byte, createdBy uuid.UUID, expiresAt *time.Time, maxUses *int) (*entity.InviteLink, error)
	GetInviteLinkByToken(ctx context.Context, tokenHash []byte) (*entity.InviteLink, error)
	ListInviteLinks(ctx context.Context, roomID uuid.UUID) ([]entity.InviteLink, error)
	RevokeInviteLink(ctx context.Context, roomID, id uuid.UUID) error
	UseInviteLink(ctx context.Context, linkID, userID uuid.UUID, now time.Time) error
}

const maxInviteLinkUses = 1000

// expiryChoices are the only lifetimes the form offers; "" means never.
var expiryChoices = map[string]time.Duration{"": 0, "1d": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour}

type createInviteLinkRequest struct {
	ExpiresIn string `json:"expiresIn"`
	MaxUses   *int   `json:"maxUses"`
}

type inviteLinkResponse struct {
	ID        uuid.UUID  `json:"id"`
	URL       string     `json:"url,omitempty"` // only when created
	CreatedBy string     `json:"createdBy"`
	ExpiresAt *time.Time `json:"expiresAt"`
	MaxUses   *int       `json:"maxUses"`
	Uses      int        `json:"uses"`
	RevokedAt *time.Time `json:"revokedAt"`
	CreatedAt time.Time  `json:"createdAt"`
}

func toInviteLinkResponse(l *entity.InviteLink) inviteLinkResponse {
	return inviteLinkResponse{ID: l.ID, CreatedBy: l.Creator, ExpiresAt: l.ExpiresAt, MaxUses: l.MaxUses, Uses: l.Uses, RevokedAt: l.RevokedAt, CreatedAt: l.CreatedAt}
}

func hashInviteToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// handleCreateInviteLink mints a link; the token is returned once.
func (s *Server) handleCreateInviteLink(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.loadRoom(w, r)
	if !ok || !s.require(w, rc, access.Invite) {
		return
	}
	var req createInviteLinkRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	ttl, ok := expiryChoices[req.ExpiresIn]
	if !ok {
		writeError(w, http.StatusBadRequest, "expiresIn must be 1d, 7d, 30d or empty")
		return
	}
	if req.MaxUses != nil && (*req.MaxUses < 1 || *req.MaxUses > maxInviteLinkUses) {
		writeError(w, http.StatusBadRequest, "maxUses must be between 1 and 1000")
		return
	}
	if !allowUser(w, s.deps.InviteLimiter, rc.actor.User.ID) {
		return
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		s.internalError(w, r, "generate token", err)
		return
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	var expiresAt *time.Time
	if ttl > 0 {
		t := time.Now().Add(ttl)
		expiresAt = &t
	}
	link, err := s.deps.InviteLinks.CreateInviteLink(r.Context(), rc.room.ID, hashInviteToken(token), rc.actor.User.ID, expiresAt, req.MaxUses)
	if err != nil {
		s.internalError(w, r, "create invite link", err)
		return
	}
	s.audit(r, "invite.link.create", "invite_link", link.ID.String(), rc.room, map[string]any{"expiresIn": req.ExpiresIn, "maxUses": req.MaxUses})

	resp := toInviteLinkResponse(link)
	resp.URL = s.joinURL(r, token)
	writeJSON(w, http.StatusCreated, resp)
}

// joinURL builds the public link from the request's origin.
func (s *Server) joinURL(r *http.Request, token string) string {
	scheme := "http"
	if isHTTPS(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host + "/join/" + token
}

func (s *Server) handleListInviteLinks(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.loadRoom(w, r)
	if !ok || !s.require(w, rc, access.Invite) {
		return
	}
	links, err := s.deps.InviteLinks.ListInviteLinks(r.Context(), rc.room.ID)
	if err != nil {
		s.internalError(w, r, "list invite links", err)
		return
	}
	out := make([]inviteLinkResponse, 0, len(links))
	for i := range links {
		out = append(out, toInviteLinkResponse(&links[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleRevokeInviteLink(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.loadRoom(w, r)
	if !ok || !s.require(w, rc, access.Invite) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "invite link not found")
		return
	}
	err = s.deps.InviteLinks.RevokeInviteLink(r.Context(), rc.room.ID, id)
	switch {
	case errors.Is(err, repository.ErrNotFound):
		writeError(w, http.StatusNotFound, "invite link not found")
		return
	case err != nil:
		s.internalError(w, r, "revoke invite link", err)
		return
	}
	s.audit(r, "invite.link.revoke", "invite_link", id.String(), rc.room, nil)
	w.WriteHeader(http.StatusNoContent)
}

type joinPreviewResponse struct {
	RoomSlug string `json:"roomSlug"`
	RoomName string `json:"roomName"`
	Valid    bool   `json:"valid"`
	Reason   string `json:"reason,omitempty"` // revoked, expired, used up, banned
	Member   bool   `json:"member"`           // the caller already belongs
}

// loadLink resolves {token}; unknown tokens are 404.
func (s *Server) loadLink(w http.ResponseWriter, r *http.Request) (*entity.InviteLink, bool) {
	token := chi.URLParam(r, "token")
	if len(token) < 16 || len(token) > 128 {
		writeError(w, http.StatusNotFound, "invite link not found")
		return nil, false
	}
	link, err := s.deps.InviteLinks.GetInviteLinkByToken(r.Context(), hashInviteToken(token))
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, "invite link not found")
		return nil, false
	}
	if err != nil {
		s.internalError(w, r, "load invite link", err)
		return nil, false
	}
	return link, true
}

// handleJoinPreview tells the landing page what the link opens and
// whether it still works for this caller.
func (s *Server) handleJoinPreview(w http.ResponseWriter, r *http.Request) {
	link, ok := s.loadLink(w, r)
	if !ok {
		return
	}
	resp := joinPreviewResponse{RoomSlug: link.RoomSlug, RoomName: link.RoomName}
	resp.Valid, resp.Reason = link.Usable(time.Now())
	if u := auth.UserFrom(r.Context()); u != nil {
		if _, err := s.deps.Rooms.GetMember(r.Context(), link.RoomID, u.ID); err == nil {
			resp.Member = true
		}
		if _, err := s.deps.Rooms.GetBan(r.Context(), link.RoomID, u.ID); err == nil {
			resp.Valid, resp.Reason = false, "banned"
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleJoin admits the caller through the link.
func (s *Server) handleJoin(w http.ResponseWriter, r *http.Request) {
	link, ok := s.loadLink(w, r)
	if !ok {
		return
	}
	u := auth.UserFrom(r.Context())
	if _, err := s.deps.Rooms.GetBan(r.Context(), link.RoomID, u.ID); err == nil {
		writeError(w, http.StatusForbidden, "you are banned from this room")
		return
	}
	// Existing members just go in; the link is not charged.
	if _, err := s.deps.Rooms.GetMember(r.Context(), link.RoomID, u.ID); err == nil {
		writeJSON(w, http.StatusOK, map[string]string{"roomSlug": link.RoomSlug})
		return
	}
	if ok, reason := link.Usable(time.Now()); !ok {
		writeError(w, http.StatusGone, "this invite link is "+reason)
		return
	}
	err := s.deps.InviteLinks.UseInviteLink(r.Context(), link.ID, u.ID, time.Now())
	switch {
	case errors.Is(err, repository.ErrConflict):
		writeError(w, http.StatusGone, "this invite link is used up")
		return
	case err != nil:
		s.internalError(w, r, "use invite link", err)
		return
	}
	room := &entity.Room{ID: link.RoomID, Slug: link.RoomSlug, Name: link.RoomName}
	s.audit(r, "invite.link.join", "invite_link", link.ID.String(), room, nil)
	if s.deps.OnRoomChanged != nil {
		s.deps.OnRoomChanged(link.RoomID)
	}
	writeJSON(w, http.StatusOK, map[string]string{"roomSlug": link.RoomSlug})
}
