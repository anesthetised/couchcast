package apihttp

import (
	"context"
	"net/http"

	"uuid"

	"github.com/anesthetised/couchcast/internal/access"
)

// StarStore persists starred rooms.
type StarStore interface {
	SetStar(ctx context.Context, userID, roomID uuid.UUID, on bool) error
	IsStarred(ctx context.Context, userID, roomID uuid.UUID) (bool, error)
}

// handleStar and handleUnstar toggle the caller's star; anyone who may
// view the room may star it.
func (s *Server) handleStar(w http.ResponseWriter, r *http.Request) {
	s.setStar(w, r, true)
}

func (s *Server) handleUnstar(w http.ResponseWriter, r *http.Request) {
	s.setStar(w, r, false)
}

func (s *Server) setStar(w http.ResponseWriter, r *http.Request, on bool) {
	rc, ok := s.loadRoom(w, r)
	if !ok || !s.require(w, rc, access.ViewRoom) {
		return
	}
	if err := s.deps.Stars.SetStar(r.Context(), rc.actor.User.ID, rc.room.ID, on); err != nil {
		s.internalError(w, r, "set star", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
