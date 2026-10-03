package server

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// spaHandler serves the embedded frontend and falls back to index.html for
// client-side routes. Deep links such as /projects/{id} are handled by the
// browser's History API, so a page refresh must return the app shell rather
// than a 404.
func spaHandler(static fs.FS) http.Handler {
	files := http.FileServer(http.FS(static))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// API and asset requests behave normally.
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		requested := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if requested == "" || requested == "." {
			serveIndex(w, r, static, files)
			return
		}
		if info, err := fs.Stat(static, requested); err == nil && !info.IsDir() {
			files.ServeHTTP(w, r)
			return
		}
		// A missing path with a file extension is a genuine 404 (a stale
		// asset link should not silently receive HTML).
		if path.Ext(requested) != "" {
			http.NotFound(w, r)
			return
		}
		serveIndex(w, r, static, files)
	})
}

func serveIndex(w http.ResponseWriter, r *http.Request, static fs.FS, files http.Handler) {
	clone := r.Clone(r.Context())
	clone.URL.Path = "/"
	files.ServeHTTP(w, clone)
	_ = static
}
