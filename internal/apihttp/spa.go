package apihttp

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// spaHandler serves the built frontend. Paths that do not match a file fall
// back to index.html so that client-side routes work on reload. When the
// bundle has not been built (development runs Vite instead) it answers 404
// with a hint rather than an empty page.
func spaHandler(static fs.FS) http.HandlerFunc {
	files := http.FS(static)
	fileServer := http.FileServer(files)

	_, indexErr := fs.Stat(static, "index.html")
	hasIndex := indexErr == nil

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
			if name == "index.html" {
				// The shell must never be cached: hashed assets are.
				w.Header().Set("Cache-Control", "no-cache")
			}
			fileServer.ServeHTTP(w, r)
			return
		}

		if !hasIndex {
			http.Error(w, "frontend bundle is not built; run the Vite dev server or `just build`", http.StatusNotFound)
			return
		}

		w.Header().Set("Cache-Control", "no-cache")
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	}
}
