package httpapi

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Static assets are a convenience, never a coupling. The API serves JSON under its own
// base path and knows nothing about a user interface; when an operator points the
// server at a built bundle it is served from the root as a courtesy, so one command
// brings up a working local stack. The same bundle can sit behind object storage and a
// CDN with no change to the API, because nothing in the API's contract mentions it.

// rootHandler serves the built application when one is configured, and otherwise
// answers with a problem document that says where the API is. It never serves anything
// under the API's base path: those paths are answered by the API's own routing.
func (s *Server) rootHandler() http.Handler {
	if s.webDir == "" {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.writeProblem(w, r, codeNotFound,
				"this server publishes a JSON API at "+s.basePath+" and no static assets are configured")
		})
	}
	return http.HandlerFunc(s.serveAsset)
}

// serveAsset serves one file from the configured directory, falling back to the
// application's entry document so a client-side route (a deep link into the
// application) loads the application instead of a 404.
//
// The fallback is deliberately limited to requests that look like navigation: a
// missing script or image must fail as missing, or a build error would silently
// present itself as an HTML document where JavaScript was expected.
func (s *Server) serveAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		s.writeProblem(w, r, codeMethodNotAllowed, "this resource offers: GET, HEAD")
		return
	}
	relative := strings.TrimPrefix(r.URL.Path, "/")
	if relative == "" {
		relative = "."
	}
	localPath := filepath.FromSlash(relative)
	if !filepath.IsLocal(localPath) {
		s.writeProblem(w, r, codeNotFound, "no such asset")
		return
	}
	target := filepath.Join(s.webDir, localPath)
	requestPath := "/" + filepath.ToSlash(localPath)

	info, err := os.Stat(target)
	switch {
	case err == nil && info.IsDir():
		s.serveEntryDocument(w, r)
		return
	case err != nil:
		if filepath.Ext(localPath) != "" {
			// A missing file with an extension is a missing file, not a route.
			s.writeProblem(w, r, codeNotFound, "no such asset")
			return
		}
		s.serveEntryDocument(w, r)
		return
	}

	s.applyAssetHeaders(w, requestPath)
	http.ServeFile(w, r, target)
}

// serveEntryDocument serves the application's index document, which is what a
// client-side route resolves to.
func (s *Server) serveEntryDocument(w http.ResponseWriter, r *http.Request) {
	index := filepath.Join(s.webDir, "index.html")
	if _, err := os.Stat(index); err != nil {
		s.writeProblem(w, r, codeNotFound, "no such asset")
		return
	}
	s.applyAssetHeaders(w, "/index.html")
	http.ServeFile(w, r, index)
}

// applyAssetHeaders sets the caching and the content policy an application bundle
// needs.
//
// The two halves of the caching rule are what let the same bundle sit behind a CDN
// later: a build's asset filenames carry a content hash, so they can be cached
// forever, while the entry document must always be revalidated or a client would keep
// loading yesterday's application. The content policy is relaxed from the API's
// "nothing at all" to what a self-contained application needs, and no further — no
// third-party origin, no framing, no plugins.
func (s *Server) applyAssetHeaders(w http.ResponseWriter, requestPath string) {
	header := w.Header()
	if strings.HasSuffix(requestPath, ".html") {
		header.Set("Cache-Control", "no-cache")
	} else {
		header.Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	header.Set("Content-Security-Policy", strings.Join([]string{
		"default-src 'self'",
		"img-src 'self' data:",
		"style-src 'self' 'unsafe-inline'",
		"font-src 'self'",
		"connect-src 'self'",
		"object-src 'none'",
		"frame-ancestors 'none'",
		"base-uri 'none'",
	}, "; "))
}
