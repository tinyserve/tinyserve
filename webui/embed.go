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

	// Pre-read index.html to serve directly and avoid http.FileServer redirect loop.
	indexHTML, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		panic(err)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Serve index.html directly to avoid FileServer's redirect from /index.html to /
		if r.URL.Path == "/" || r.URL.Path == "" || r.URL.Path == "/index.html" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(indexHTML)
			return
		}
		// Prevent directory traversal attempts.
		if strings.Contains(r.URL.Path, "..") {
			http.NotFound(w, r)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}
