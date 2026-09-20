package apihttp

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"uuid"

	"github.com/anesthetised/couchcast/internal/auth"
	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/protocol"
	"github.com/anesthetised/couchcast/internal/repository"
)

// DirectoryStore lists rooms visible to a viewer.
type DirectoryStore interface {
	ListDirectory(ctx context.Context, q repository.DirectoryQuery) ([]repository.DirectoryRoom, int, error)
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
	Visibility  entity.Visibility  `json:"visibility"`
	Description string             `json:"description,omitempty"`
	MyRole      entity.RoomRole    `json:"myRole,omitempty"`
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

// handleDirectory serves the home page directory: public rooms plus the
// private rooms the caller belongs to, with what is playing, viewer
// counts, search, filters (live: a ready video is playing; private; mine)
// and pagination. Anonymous access is intended.
func (s *Server) handleDirectory(w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()
	user := auth.UserFrom(r.Context())

	search := strings.TrimSpace(qs.Get("q"))
	if len(search) > directoryMaxSearch {
		search = search[:directoryMaxSearch]
	}
	flag := func(name string) bool { v := qs.Get(name); return v == "1" || v == "true" }
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

	query := repository.DirectoryQuery{
		Search: search, OnlyLive: flag("live"), OnlyPrivate: flag("private"), OnlyMine: flag("mine"),
		Offset: (page - 1) * perPage, Limit: perPage,
	}
	if user != nil {
		query.ViewerID = &user.ID
	}
	if s.deps.Live != nil {
		for id, n := range s.deps.Live.LiveCounts() {
			query.LiveIDs = append(query.LiveIDs, id)
			query.LiveCounts = append(query.LiveCounts, n)
		}
	}

	rooms, total, err := s.deps.Directory.ListDirectory(r.Context(), query)
	if err != nil {
		s.internalError(w, r, "list directory", err)
		return
	}

	now := time.Now()
	resp := directoryResponse{ServerNowMs: now.UnixMilli(), Page: page, PerPage: perPage, Total: total, Rooms: make([]directoryRoom, 0, len(rooms))}
	for _, pr := range rooms {
		dr := directoryRoom{
			Slug: pr.Room.Slug, Name: pr.Room.Name, Owner: pr.Owner, Visibility: pr.Room.Visibility, Description: pr.Room.Description, MyRole: pr.MyRole,
			Viewers: pr.Viewers, MemberCount: pr.MemberCount, Live: pr.Live,
		}
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
