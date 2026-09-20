package apihttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"uuid"

	"github.com/anesthetised/couchcast/internal/access"
	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/repository"
)

// MuteStore is what the mute handlers need.
type MuteStore interface {
	GetMute(ctx context.Context, roomID, userID uuid.UUID) (*entity.RoomMute, error)
	ListMutes(ctx context.Context, roomID uuid.UUID, now time.Time) ([]entity.RoomMute, error)
	SetMute(ctx context.Context, roomID, userID, mutedBy uuid.UUID, reason string, until time.Time) error
	DeleteMute(ctx context.Context, roomID, userID uuid.UUID) error
}

// muteChoices are the durations the UI offers, in minutes.
var muteChoices = map[int]bool{5: true, 30: true, 60: true, 1440: true}

type muteRequest struct {
	Minutes int    `json:"minutes"`
	Reason  string `json:"reason"`
}

type muteResponse struct {
	Username  string    `json:"username"`
	Reason    string    `json:"reason"`
	Until     time.Time `json:"until"`
	CreatedAt time.Time `json:"createdAt"`
}

func (s *Server) handleListMutes(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.loadRoom(w, r)
	if !ok || !s.require(w, rc, access.ModerateChat) {
		return
	}
	mutes, err := s.deps.Mutes.ListMutes(r.Context(), rc.room.ID, time.Now())
	if err != nil {
		s.internalError(w, r, "list mutes", err)
		return
	}
	out := make([]muteResponse, 0, len(mutes))
	for _, m := range mutes {
		out = append(out, muteResponse{Username: m.Username, Reason: m.Reason, Until: m.Until, CreatedAt: m.CreatedAt})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleMute silences a member for a fixed time; moderators may mute plain
// members, only the owner may mute moderators (as with bans).
func (s *Server) handleMute(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.loadRoom(w, r)
	if !ok {
		return
	}
	target, ok := s.targetUser(w, r)
	if !ok {
		return
	}
	if target.ID == rc.actor.User.ID {
		writeError(w, http.StatusBadRequest, "you cannot mute yourself")
		return
	}
	role, err := s.targetRole(r.Context(), rc.room, target.ID)
	if err != nil {
		s.internalError(w, r, "target role", err)
		return
	}
	if !access.CanTarget(rc.actor, access.ModerateChat, rc.room, role) {
		s.denied(w, rc)
		return
	}
	var req muteRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if !muteChoices[req.Minutes] {
		writeError(w, http.StatusBadRequest, "minutes must be 5, 30, 60 or 1440")
		return
	}
	if len(req.Reason) > 200 {
		writeError(w, http.StatusBadRequest, "reason must be at most 200 characters")
		return
	}
	until := time.Now().Add(time.Duration(req.Minutes) * time.Minute)
	if err := s.deps.Mutes.SetMute(r.Context(), rc.room.ID, target.ID, rc.actor.User.ID, req.Reason, until); err != nil {
		s.internalError(w, r, "mute member", err)
		return
	}
	s.audit(r, "member.mute", "user", target.ID.String(), rc.room, map[string]any{"username": target.Username, "minutes": req.Minutes, "reason": req.Reason})
	if s.deps.OnMute != nil {
		s.deps.OnMute(rc.room.ID, target.ID, fmt.Sprintf("%s muted %s for %s", rc.actor.User.Username, target.Username, muteLabel(req.Minutes)))
	}
	writeJSON(w, http.StatusOK, muteResponse{Username: target.Username, Reason: req.Reason, Until: until, CreatedAt: time.Now()})
}

func (s *Server) handleUnmute(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.loadRoom(w, r)
	if !ok || !s.require(w, rc, access.ModerateChat) {
		return
	}
	target, ok := s.targetUser(w, r)
	if !ok {
		return
	}
	err := s.deps.Mutes.DeleteMute(r.Context(), rc.room.ID, target.ID)
	switch {
	case errors.Is(err, repository.ErrNotFound):
		writeError(w, http.StatusNotFound, "user is not muted")
		return
	case err != nil:
		s.internalError(w, r, "unmute member", err)
		return
	}
	s.audit(r, "member.unmute", "user", target.ID.String(), rc.room, map[string]any{"username": target.Username})
	if s.deps.OnMute != nil {
		s.deps.OnMute(rc.room.ID, target.ID, rc.actor.User.Username+" unmuted "+target.Username)
	}
	w.WriteHeader(http.StatusNoContent)
}

func muteLabel(minutes int) string {
	switch {
	case minutes >= 1440:
		return "a day"
	case minutes >= 60:
		return "an hour"
	default:
		return fmt.Sprintf("%d min", minutes)
	}
}
