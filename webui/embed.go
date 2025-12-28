package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed * *.html
var content embed.FS

// Handler serves the embedded web UI bundle. It falls back to index.html for the root path.
func Handler() http.Handler {
	sub, err := fs.Sub(content, ".")
	if err != nil {
		// In practice this should never fail; panic is acceptable at startup.
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "" {
			r = r.Clone(r.Context())
			r.URL.Path = "/index.html"
		}
		// Prevent directory traversal attempts.
		if strings.Contains(r.URL.Path, "..") {
			http.NotFound(w, r)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}
