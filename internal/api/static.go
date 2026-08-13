// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
)

// staticHandler serves the built frontend from dir, falling back to index.html
// for paths the build does not contain so client-side routing works on a hard
// refresh.
//
// It reports false when dir has no index.html, which is the normal state
// during frontend development. The caller then runs API-only and Vite serves
// the app.
func staticHandler(dir string) (http.HandlerFunc, bool) {
	if dir == "" {
		return nil, false
	}
	if _, err := os.Stat(path.Join(dir, "index.html")); err != nil {
		return nil, false
	}

	root := os.DirFS(dir)
	files := http.FileServerFS(root)

	return func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")

		// fs.ValidPath rejects traversal and absolute paths, so a request for
		// ../../etc/passwd falls through to index.html instead of escaping.
		if name != "" && fs.ValidPath(name) {
			if info, err := fs.Stat(root, name); err == nil && !info.IsDir() {
				files.ServeHTTP(w, r)
				return
			}
		}

		// Unknown path: hand the SPA its entrypoint and let the router decide.
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFileFS(w, r, root, "index.html")
	}, true
}
