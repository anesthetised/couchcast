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
	"github.com/anesthetised/couchcast/internal/ratelimit"
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

// Answers for links the server refuses to fetch (see internal/netguard).
const (
	msgPrivateAddress = "links to private or local addresses are not allowed"
	msgUnknownHost    = "this site could not be found"
)

// handleProbe answers GET /api/v1/media/probe?url= for the add form:
// title, duration and thumbnail, plus the ingest status when the video
// is already known. Signed in, rate limited per user and per address, and
// bounded in how many run at once.
func (s *Server) handleProbe(w http.ResponseWriter, r *http.Request) {
	rawURL := strings.TrimSpace(r.URL.Query().Get("url"))
	if rawURL == "" || len(rawURL) > 2048 {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}
	release, ok := s.admitProbe(w, r)
	if !ok {
		return
	}
	defer release()

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
	case errors.Is(err, ingest.ErrPrivateAddress):
		writeError(w, http.StatusBadRequest, msgPrivateAddress)
		return
	case errors.Is(err, ingest.ErrUnknownHost):
		writeError(w, http.StatusBadRequest, msgUnknownHost)
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

// admitProbe applies the preview budgets (per user, per address) and
// takes one of the concurrent slots; release frees it.
func (s *Server) admitProbe(w http.ResponseWriter, r *http.Request) (release func(), ok bool) {
	if !allowUser(w, s.deps.ProbeLimiter, auth.UserFrom(r.Context()).ID) {
		return nil, false
	}
	if l := s.deps.ProbeIPLimiter; l != nil && !l.Allow(ratelimit.ClientIP(s.deps.TrustProxy)(r)) {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "too many link checks from your network; try again later")
		return nil, false
	}
	if s.probeSlots == nil {
		return func() {}, true
	}
	t := time.NewTimer(s.deps.ProbeWait)
	defer t.Stop()
	select {
	case s.probeSlots <- struct{}{}:
		return func() { <-s.probeSlots }, true
	case <-t.C:
	case <-r.Context().Done():
	}
	w.Header().Set("Retry-After", "5")
	writeError(w, http.StatusServiceUnavailable, "the server is busy checking other links; try again in a moment")
	return nil, false
}

// handlePlaylist answers GET /api/v1/media/playlist?url= with the first
// entries of a playlist, for the import picker. It spends the probe budget.
func (s *Server) handlePlaylist(w http.ResponseWriter, r *http.Request) {
	rawURL := strings.TrimSpace(r.URL.Query().Get("url"))
	if rawURL == "" || len(rawURL) > 2048 {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}
	release, ok := s.admitProbe(w, r)
	if !ok {
		return
	}
	defer release()
	ctx, cancel := context.WithTimeout(r.Context(), playlistTimeout)
	defer cancel()
	pl, err := s.deps.Prober.Playlist(ctx, rawURL)
	switch {
	case errors.Is(err, ingest.ErrUnsupportedURL):
		writeError(w, http.StatusBadRequest, "this is not a playlist link")
		return
	case errors.Is(err, ingest.ErrPrivateAddress):
		writeError(w, http.StatusBadRequest, msgPrivateAddress)
		return
	case errors.Is(err, ingest.ErrUnknownHost):
		writeError(w, http.StatusBadRequest, msgUnknownHost)
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
