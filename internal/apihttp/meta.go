package apihttp

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"uuid"

	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/repository"
)

// MetaStore is what link previews need: the room and what it is playing.
type MetaStore interface {
	GetRoomBySlug(ctx context.Context, slug string) (*entity.Room, error)
	GetCurrentMedia(ctx context.Context, roomID uuid.UUID) (*entity.Media, error)
}

// metaTTL bounds how often a crawler storm can hit the database per room.
const metaTTL = 30 * time.Second

// metaInjector adds Open Graph tags to index.html for public room pages,
// so a shared link unfurls with the room's name, description and the
// current video's thumbnail in messengers.
type metaInjector struct {
	store  MetaStore
	live   LiveRooms
	logger *slog.Logger
	site   string

	mu    sync.Mutex
	cache map[string]metaEntry
}

type metaEntry struct {
	tags string
	at   time.Time
}

func newMetaInjector(store MetaStore, live LiveRooms, logger *slog.Logger) *metaInjector {
	return &metaInjector{store: store, live: live, logger: logger, site: "couchcast", cache: map[string]metaEntry{}}
}

// roomSlugFromPath returns the slug for /r/{slug} pages, "" otherwise.
func roomSlugFromPath(p string) string {
	rest, ok := strings.CutPrefix(p, "/r/")
	if !ok || rest == "" || strings.Contains(rest, "/") {
		return ""
	}
	return rest
}

// tagsFor builds the <meta> block for a request, cached per slug.
func (m *metaInjector) tagsFor(r *http.Request, slug string) string {
	m.mu.Lock()
	if e, ok := m.cache[slug]; ok && time.Since(e.at) < metaTTL {
		m.mu.Unlock()
		return e.tags
	}
	m.mu.Unlock()

	tags := m.build(r, slug)

	m.mu.Lock()
	m.cache[slug] = metaEntry{tags: tags, at: time.Now()}
	m.mu.Unlock()
	return tags
}

func (m *metaInjector) build(r *http.Request, slug string) string {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	room, err := m.store.GetRoomBySlug(ctx, slug)
	if err != nil {
		if !errors.Is(err, repository.ErrNotFound) {
			m.logger.Warn("room meta: load room", "slug", slug, "error", err)
		}
		return ""
	}
	if !room.IsPublic() {
		return "" // private rooms unfurl as the generic site
	}

	title := room.Name
	desc := room.Description
	image := ""
	viewers := 0
	if m.live != nil {
		viewers = m.live.LiveCounts()[room.ID]
	}
	if media, err := m.store.GetCurrentMedia(ctx, room.ID); err == nil {
		image = media.ThumbnailURL
		if room.Playing && media.IsReady() && media.Title != "" {
			playing := fmt.Sprintf("Playing “%s” · %d watching", media.Title, viewers)
			if desc == "" {
				desc = playing
			} else {
				desc += " · " + playing
			}
		}
	} else if err != nil && !errors.Is(err, repository.ErrNotFound) {
		m.logger.Warn("room meta: current media", "slug", slug, "error", err)
		return ""
	}
	if desc == "" {
		desc = "Watch together, in sync."
	}

	scheme := "http"
	if isHTTPS(r) {
		scheme = "https"
	}
	url := scheme + "://" + r.Host + "/r/" + slug
	// A self-hosted poster is a path; crawlers need it absolute.
	if strings.HasPrefix(image, "/") {
		image = scheme + "://" + r.Host + image
	}

	var b strings.Builder
	tag := func(prop, content string) {
		fmt.Fprintf(&b, `<meta property="%s" content="%s">`+"\n", prop, html.EscapeString(content))
	}
	tag("og:type", "website")
	tag("og:site_name", m.site)
	tag("og:title", title)
	tag("og:description", desc)
	tag("og:url", url)
	if image != "" {
		tag("og:image", image)
		fmt.Fprintf(&b, `<meta name="twitter:card" content="summary_large_image">`+"\n")
	} else {
		fmt.Fprintf(&b, `<meta name="twitter:card" content="summary">`+"\n")
	}
	fmt.Fprintf(&b, `<meta name="description" content="%s">`+"\n", html.EscapeString(desc))
	// The <title> is what the SPA sets too; crawlers do not run scripts.
	fmt.Fprintf(&b, "<title>%s · %s</title>\n", html.EscapeString(title), m.site)
	return b.String()
}

// inject puts the tags before </head>; the default <title> is replaced
// when the tags carry their own.
func injectMeta(index []byte, tags string) []byte {
	if tags == "" {
		return index
	}
	s := string(index)
	if strings.Contains(tags, "<title>") {
		if i := strings.Index(s, "<title>"); i >= 0 {
			if j := strings.Index(s[i:], "</title>"); j >= 0 {
				s = s[:i] + s[i+j+len("</title>"):]
			}
		}
	}
	i := strings.Index(s, "</head>")
	if i < 0 {
		return index
	}
	return []byte(s[:i] + tags + s[i:])
}
