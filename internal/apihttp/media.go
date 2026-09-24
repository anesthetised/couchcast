package apihttp

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/anesthetised/couchcast/internal/auth"
	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/ingest"
	"github.com/anesthetised/couchcast/internal/source"
)

// probeTimeout bounds one extractor call; yt-dlp normally answers in a
// few seconds and the form should not hang on a slow site.
const probeTimeout = 15 * time.Second

// Prober describes a URL without queueing it (ingest.Service.Preview).
type Prober interface {
	Preview(ctx context.Context, rawURL string) (*ingest.Preview, error)
	PlaylistURL(rawURL string) (playlist string, video bool, ok bool)
	Playlist(ctx context.Context, rawURL string) (*source.Playlist, error)
}

type probeResponse struct {
	Title        string             `json:"title"`
	DurationMs   int64              `json:"durationMs"`
	ThumbnailURL string             `json:"thumbnailUrl,omitempty"`
	Status       entity.MediaStatus `json:"status,omitempty"`
	// PlaylistURL is set when the link names a playlist; a link that
	// names only the playlist (no video) carries nothing else.
	PlaylistURL string `json:"playlistUrl,omitempty"`
}

type playlistEntryResponse struct {
	URL          string `json:"url"`
	Title        string `json:"title"`
	DurationMs   int64  `json:"durationMs"`
	ThumbnailURL string `json:"thumbnailUrl,omitempty"`
}

type playlistResponse struct {
	Title   string                  `json:"title"`
	Total   int                     `json:"total"`
	Entries []playlistEntryResponse `json:"entries"`
}

// playlistTimeout is longer than a probe: a playlist page is bigger.
const playlistTimeout = 30 * time.Second

// handleProbe answers GET /api/v1/media/probe?url= for the add form:
// title, duration and thumbnail, plus the ingest status when the video
// is already known. Signed in and rate limited per user.
func (s *Server) handleProbe(w http.ResponseWriter, r *http.Request) {
	rawURL := strings.TrimSpace(r.URL.Query().Get("url"))
	if rawURL == "" || len(rawURL) > 2048 {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}
	if !allowUser(w, s.deps.ProbeLimiter, auth.UserFrom(r.Context()).ID) {
		return
	}

	playlist, video, isList := s.deps.Prober.PlaylistURL(rawURL)
	if isList && !video {
		writeJSON(w, http.StatusOK, probeResponse{PlaylistURL: playlist})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
	defer cancel()
	p, err := s.deps.Prober.Preview(ctx, rawURL)
	switch {
	case errors.Is(err, ingest.ErrUnsupportedURL):
		writeError(w, http.StatusBadRequest, "this link is not supported")
		return
	case errors.Is(err, ingest.ErrBlocked):
		writeError(w, http.StatusBadRequest, "this video has been blocked by an administrator")
		return
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, "the site did not answer in time")
		return
	case err != nil:
		// Extractor failures (private video, geo block, …) are the
		// user's problem to fix, not ours to log as errors.
		s.deps.Logger.InfoContext(r.Context(), "probe failed", "url", rawURL, "error", err)
		writeError(w, http.StatusUnprocessableEntity, "could not read this video")
		return
	}
	resp := probeResponse{Title: p.Title, DurationMs: p.DurationMs, ThumbnailURL: p.ThumbnailURL, Status: p.Status}
	if isList {
		resp.PlaylistURL = playlist
	}
	writeJSON(w, http.StatusOK, resp)
}

// handlePlaylist answers GET /api/v1/media/playlist?url= with the first
// entries of a playlist, for the import picker. It spends the probe budget.
func (s *Server) handlePlaylist(w http.ResponseWriter, r *http.Request) {
	rawURL := strings.TrimSpace(r.URL.Query().Get("url"))
	if rawURL == "" || len(rawURL) > 2048 {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}
	if !allowUser(w, s.deps.ProbeLimiter, auth.UserFrom(r.Context()).ID) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), playlistTimeout)
	defer cancel()
	pl, err := s.deps.Prober.Playlist(ctx, rawURL)
	switch {
	case errors.Is(err, ingest.ErrUnsupportedURL):
		writeError(w, http.StatusBadRequest, "this is not a playlist link")
		return
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, "the site did not answer in time")
		return
	case err != nil:
		s.deps.Logger.InfoContext(r.Context(), "playlist failed", "url", rawURL, "error", err)
		writeError(w, http.StatusUnprocessableEntity, "could not read this playlist")
		return
	}
	resp := playlistResponse{Title: pl.Title, Total: pl.Total, Entries: make([]playlistEntryResponse, 0, len(pl.Entries))}
	for _, e := range pl.Entries {
		resp.Entries = append(resp.Entries, playlistEntryResponse{URL: e.URL, Title: e.Title, DurationMs: e.DurationMs, ThumbnailURL: e.ThumbnailURL})
	}
	writeJSON(w, http.StatusOK, resp)
}
