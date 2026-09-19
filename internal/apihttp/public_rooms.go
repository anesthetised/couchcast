package apihttp

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"uuid"

	"github.com/anesthetised/couchcast/internal/protocol"
	"github.com/anesthetised/couchcast/internal/repository"
)

// DirectoryStore lists public rooms.
type DirectoryStore interface {
	ListPublicRooms(ctx context.Context, q repository.PublicRoomsQuery) ([]repository.PublicRoom, int, error)
}

// LiveRooms exposes what the room manager knows about loaded rooms.
type LiveRooms interface {
	LiveCounts() map[uuid.UUID]int
	Playback(roomID uuid.UUID) (protocol.Playback, bool)
}

const (
	directoryDefaultPerPage = 24
	directoryMaxPerPage     = 48
	directoryMaxSearch      = 64
)

type directoryMedia struct {
	ID           uuid.UUID `json:"id"`
	Title        string    `json:"title"`
	ThumbnailURL string    `json:"thumbnailUrl"`
	DurationMs   int64     `json:"durationMs"`
	Manifest     string    `json:"manifest,omitempty"`
	Token        string    `json:"token,omitempty"`
}

type directoryPlayback struct {
	Playing    bool  `json:"playing"`
	PositionMs int64 `json:"positionMs"`
	AtServerMs int64 `json:"atServerMs"`
}

type directoryRoom struct {
	Slug        string             `json:"slug"`
	Name        string             `json:"name"`
	Owner       string             `json:"owner"`
	Viewers     int                `json:"viewers"`
	MemberCount int                `json:"memberCount"`
	Live        bool               `json:"live"`
	Media       *directoryMedia    `json:"media"`
	Playback    *directoryPlayback `json:"playback"`
}

type directoryResponse struct {
	ServerNowMs int64           `json:"serverNowMs"`
	Page        int             `json:"page"`
	PerPage     int             `json:"perPage"`
	Total       int             `json:"total"`
	Rooms       []directoryRoom `json:"rooms"`
}

// handlePublicRooms serves the directory on the home page: public rooms
// with what is playing, viewer counts, search, a live filter (a ready
// video is playing, watched or not) and pagination. Anonymous access is
// intended.
func (s *Server) handlePublicRooms(w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()

	search := strings.TrimSpace(qs.Get("q"))
	if len(search) > directoryMaxSearch {
		search = search[:directoryMaxSearch]
	}
	live := qs.Get("live") == "1" || qs.Get("live") == "true"
	page, _ := strconv.Atoi(qs.Get("page"))
	if page < 1 {
		page = 1
	}
	perPage, _ := strconv.Atoi(qs.Get("perPage"))
	if perPage < 1 {
		perPage = directoryDefaultPerPage
	}
	if perPage > directoryMaxPerPage {
		perPage = directoryMaxPerPage
	}

	query := repository.PublicRoomsQuery{Search: search, OnlyLive: live, Offset: (page - 1) * perPage, Limit: perPage}
	if s.deps.Live != nil {
		for id, n := range s.deps.Live.LiveCounts() {
			query.LiveIDs = append(query.LiveIDs, id)
			query.LiveCounts = append(query.LiveCounts, n)
		}
	}

	rooms, total, err := s.deps.Directory.ListPublicRooms(r.Context(), query)
	if err != nil {
		s.internalError(w, r, "list public rooms", err)
		return
	}

	now := time.Now()
	resp := directoryResponse{ServerNowMs: now.UnixMilli(), Page: page, PerPage: perPage, Total: total, Rooms: make([]directoryRoom, 0, len(rooms))}
	for _, pr := range rooms {
		dr := directoryRoom{Slug: pr.Room.Slug, Name: pr.Room.Name, Owner: pr.Owner, Viewers: pr.Viewers, MemberCount: pr.MemberCount, Live: pr.Live}
		if pr.Media != nil {
			m := &directoryMedia{ID: pr.Media.ID, Title: pr.Media.Title, ThumbnailURL: pr.Media.ThumbnailURL, DurationMs: pr.Media.DurationMs}
			if pr.Media.IsReady() && s.deps.Signer != nil {
				m.Manifest = "/media/" + pr.Media.ID.String() + "/manifest.mpd"
				m.Token = s.deps.Signer.Sign(pr.Media.ID, now)
			}
			dr.Media = m

			// Prefer the live clock; fall back to the persisted one.
			pb := directoryPlayback{Playing: pr.Room.Playing, PositionMs: pr.Room.PositionMs, AtServerMs: pr.Room.PositionAt.UnixMilli()}
			if s.deps.Live != nil {
				if livePb, ok := s.deps.Live.Playback(pr.Room.ID); ok {
					pb = directoryPlayback{Playing: livePb.Playing, PositionMs: livePb.PositionMs, AtServerMs: livePb.AtServerMs}
				}
			}
			dr.Playback = &pb
		}
		resp.Rooms = append(resp.Rooms, dr)
	}

	writeJSON(w, http.StatusOK, resp)
}
