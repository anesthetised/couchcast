package apihttp

import (
	"bytes"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

// spaHandler serves the built frontend. Paths that do not match a file fall
// back to index.html so that client-side routes work on reload. When the
// bundle has not been built (development runs Vite instead) it answers 404
// with a hint rather than an empty page.
func spaHandler(static fs.FS, meta *metaInjector) http.HandlerFunc {
	files := http.FS(static)
	fileServer := http.FileServer(files)

	index, indexErr := fs.ReadFile(static, "index.html")
	hasIndex := indexErr == nil
	started := time.Now()

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}

		if f, err := static.Open(name); err == nil {
			_ = f.Close()
			switch name {
			case "index.html", "sw.js", "manifest.webmanifest":
				// The shell, the service worker and the manifest must never
				// be cached: hashed assets are.
				w.Header().Set("Cache-Control", "no-cache")
			}
			if name == "manifest.webmanifest" {
				w.Header().Set("Content-Type", "application/manifest+json")
			}
			fileServer.ServeHTTP(w, r)
			return
		}

		if !hasIndex {
			http.Error(w, "frontend bundle is not built; run the Vite dev server or `just build`", http.StatusNotFound)
			return
		}

		w.Header().Set("Cache-Control", "no-cache")
		// Room pages get link-preview tags; everything else is the plain shell.
		if slug := roomSlugFromPath(r.URL.Path); slug != "" && meta != nil {
			body := injectMeta(index, meta.tagsFor(r, slug))
			http.ServeContent(w, r, "index.html", started, bytes.NewReader(body))
			return
		}
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	}
}
