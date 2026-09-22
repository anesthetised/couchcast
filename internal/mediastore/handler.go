package mediastore

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"uuid"

	"github.com/minio/minio-go/v7"
)

// AccessRecorder is notified when a manifest is fetched so that the
// eviction janitor knows the media is in use.
type AccessRecorder interface {
	TouchMediaAccess(ctx context.Context, id uuid.UUID, at time.Time) error
}

// Handler serves GET /media/{mediaID}/{file}?t=<token>.
type Handler struct {
	store   *Store
	signer  *Signer
	access  AccessRecorder
	logger  *slog.Logger
	onBytes func(int64)
	now     func() time.Time
}

// NewHandler creates the media proxy. onBytes may be nil.
func NewHandler(store *Store, signer *Signer, access AccessRecorder, logger *slog.Logger, onBytes func(int64)) *Handler {
	if onBytes == nil {
		onBytes = func(int64) {}
	}
	return &Handler{store: store, signer: signer, access: access, logger: logger, onBytes: onBytes, now: time.Now}
}

// ServeHTTP expects the router to have matched /media/{mediaID}/{file}
// and to pass mediaID and file via the request context helpers below, or
// simply parses them from the URL path.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	mediaID, file, ok := splitMediaPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}

	// Posters are public (cards, link previews); everything else needs
	// the media token.
	public := IsThumbnail(file)
	if !public && !h.signer.Verify(r.URL.Query().Get("t"), mediaID, h.now()) {
		http.Error(w, "invalid or expired media token", http.StatusUnauthorized)
		return
	}

	key := Prefix(mediaID.String()) + file
	obj, err := h.store.Open(r.Context(), key)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "open media object", "key", key, "error", err)
		http.Error(w, "storage error", http.StatusBadGateway)
		return
	}
	defer func() { _ = obj.Close() }()

	info, err := obj.Stat()
	if err != nil {
		var resp minio.ErrorResponse
		if errors.As(err, &resp) && resp.StatusCode == http.StatusNotFound {
			http.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "stat media object", "key", key, "error", err)
		http.Error(w, "storage error", http.StatusBadGateway)
		return
	}

	if file == "manifest.mpd" && h.access != nil {
		if err := h.access.TouchMediaAccess(r.Context(), mediaID, h.now()); err != nil {
			h.logger.WarnContext(r.Context(), "touch media access", "error", err)
		}
	}

	w.Header().Set("Content-Type", ContentType(file))
	if public {
		w.Header().Set("Cache-Control", "public, max-age=86400")
	} else {
		w.Header().Set("Cache-Control", "private, max-age=3600")
	}
	if info.ETag != "" {
		w.Header().Set("ETag", `"`+info.ETag+`"`)
	}

	cw := &countingWriter{ResponseWriter: w}
	http.ServeContent(cw, r, file, info.LastModified, obj)
	h.onBytes(cw.n)
}

// splitMediaPath parses /media/{uuid}/{file}. Nested paths are rejected so
// a token for one media cannot reach another prefix.
func splitMediaPath(p string) (uuid.UUID, string, bool) {
	p = strings.TrimPrefix(path.Clean(p), "/media/")
	idStr, file, ok := strings.Cut(p, "/")
	if !ok || file == "" || strings.Contains(file, "/") || strings.HasPrefix(file, ".") {
		return uuid.Nil(), "", false
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return uuid.Nil(), "", false
	}
	return id, file, true
}

type countingWriter struct {
	http.ResponseWriter
	n int64
}

func (c *countingWriter) Write(b []byte) (int, error) {
	n, err := c.ResponseWriter.Write(b)
	c.n += int64(n)
	return n, err
}
