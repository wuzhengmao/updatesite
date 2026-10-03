// Package server exposes the archive over HTTP: a browsable site, a JSON API
// and a download endpoint.
package server

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/wuzhengmao/updatesite/internal/buildinfo"
	"github.com/wuzhengmao/updatesite/internal/config"
	"github.com/wuzhengmao/updatesite/internal/index"
)

//go:embed templates static
var assets embed.FS

// Server wires the configuration, the index, the templates and the embedded
// documentation together.
type Server struct {
	cfg     config.Config
	idx     *index.Index
	tmpl    *template.Template
	docs    []*doc
	started time.Time
}

// New builds a server, parsing the embedded templates and rendering the
// bundled documentation once at start up.
func New(cfg config.Config, idx *index.Index) (*Server, error) {
	s := &Server{cfg: cfg, idx: idx, started: time.Now()}
	t, err := template.New("").Funcs(templateFuncs()).ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, err
	}
	s.tmpl = t

	s.docs, err = loadDocs()
	if err != nil {
		return nil, err
	}
	return s, nil
}

// Handler returns the fully wired HTTP handler.
func (s *Server) Handler() http.Handler {
	return s.recoverer(s.logger(s.cors(s.routes())))
}

func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()

	// Web site.
	mux.HandleFunc("GET /{$}", s.handleIndexPage)
	mux.HandleFunc("GET /a/{app}", s.handleAppPage)
	mux.HandleFunc("GET /a/{app}/icon", s.handleIcon)
	mux.HandleFunc("GET /a/{app}/{version}", s.handleAppPage)

	// Bundled documentation.
	mux.HandleFunc("GET /docs", s.handleDocs)
	mux.HandleFunc("GET /docs/{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/docs", http.StatusFound)
	})
	mux.HandleFunc("GET /docs/{name}", s.handleDoc)

	// Embedded assets.
	staticFS, _ := fs.Sub(assets, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", noDirListing(http.FileServer(http.FS(staticFS)))))

	// JSON API.
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/apps", s.handleApps)
	mux.HandleFunc("GET /api/v1/apps/{app}", s.handleApp)
	mux.HandleFunc("GET /api/v1/apps/{app}/releases", s.handleReleases)
	mux.HandleFunc("GET /api/v1/apps/{app}/releases/{version}", s.handleRelease)
	mux.HandleFunc("GET /api/v1/apps/{app}/latest", s.handleLatest)
	mux.HandleFunc("GET /api/v1/apps/{app}/check", s.handleCheck)
	mux.HandleFunc("POST /api/v1/rescan", s.handleRescan)
	mux.HandleFunc("POST /api/v1/apps/{app}/upload", s.handleUpload)

	// Application level metadata: app.json and the icon.
	mux.HandleFunc("GET /api/v1/apps/{app}/metadata", s.handleGetMetadata)
	mux.HandleFunc("PUT /api/v1/apps/{app}/metadata", s.handlePutMetadata)
	mux.HandleFunc("PUT /api/v1/apps/{app}/icon", s.handlePutIcon)

	// Removal. Destructive, so the token is required.
	mux.HandleFunc("DELETE /api/v1/apps/{app}/releases/{version}", s.handleDeleteRelease)
	mux.HandleFunc("DELETE /api/v1/apps/{app}", s.handleDeleteApp)

	// Upload page.
	mux.HandleFunc("GET /upload", s.handleUploadPage)

	// Downloads.
	mux.HandleFunc("GET /dl/{app}/{version}/{file...}", s.handleDownload)

	mux.HandleFunc("/", s.handleNotFound)
	return mux
}

// ---------------------------------------------------------------- middleware

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

func (s *Server) logger(next http.Handler) http.Handler {
	if !s.cfg.LogRequests {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/static/") {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		if sw.status == 0 {
			sw.status = http.StatusOK
		}
		log.Printf("%s %s %d %dB %s", r.Method, r.URL.RequestURI(), sw.status, sw.bytes,
			time.Since(start).Round(time.Millisecond))
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic serving %s: %v\n%s", r.URL.Path, rec, debug.Stack())
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) cors(next http.Handler) http.Handler {
	origin := s.cfg.CORSOrigin
	if origin == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", origin)
		h.Set("Access-Control-Allow-Methods", "GET, HEAD, POST, OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		h.Set("Access-Control-Max-Age", "86400")
		if origin != "*" {
			h.Add("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// noDirListing hides embedded directory listings.
func noDirListing(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=3600")
		next.ServeHTTP(w, r)
	})
}

// ------------------------------------------------------------------ helpers

// writeJSON serialises v, honouring ?pretty=1.
func (s *Server) writeJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	var (
		data []byte
		err  error
	)
	if _, pretty := r.URL.Query()["pretty"]; pretty {
		data, err = json.MarshalIndent(v, "", "  ")
	} else {
		data, err = json.Marshal(v)
	}
	if err != nil {
		log.Printf("json encode failed: %v", err)
		http.Error(w, `{"error":{"code":"internal","message":"encoding failed"}}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(status)
	w.Write(append(data, '\n'))
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (s *Server) writeError(w http.ResponseWriter, r *http.Request, status int, code, format string, args ...any) {
	s.writeJSON(w, r, status, map[string]apiError{
		"error": {Code: code, Message: fmt.Sprintf(format, args...)},
	})
}

// absoluteURL turns a site-relative path into an absolute URL, preferring the
// configured BASE_URL and otherwise reconstructing it from the request.
func (s *Server) absoluteURL(r *http.Request, p string) string {
	if p == "" {
		return ""
	}
	if s.cfg.BaseURL != "" {
		return s.cfg.BaseURL + p
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}
	host := r.Host
	if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
		host = fwd
	}
	if host == "" {
		return p
	}
	return scheme + "://" + host + p
}

// lookupApp resolves an application, writing a 404 when it does not exist.
func (s *Server) lookupApp(w http.ResponseWriter, r *http.Request) (*index.App, bool) {
	id := r.PathValue("app")
	app, ok := s.idx.Current().App(id)
	if !ok {
		s.writeError(w, r, http.StatusNotFound, "app_not_found", "no application named %q", id)
		return nil, false
	}
	return app, true
}

// siteInfo carries the values every page needs.
type siteInfo struct {
	Title         string
	Subtitle      string
	Version       string
	Commit        string
	BuiltAt       string
	BaseURL       string
	Year          int
	Stats         index.Stats
	AppsCount     int
	UploadEnabled bool
}

func (s *Server) site() siteInfo {
	st := s.idx.Stats()
	return siteInfo{
		Title:         s.cfg.SiteTitle,
		Subtitle:      s.cfg.SiteSubtitle,
		Version:       buildinfo.Version,
		Commit:        formatBuildCommit(buildinfo.Commit),
		BuiltAt:       formatBuildTime(buildinfo.Date),
		BaseURL:       s.cfg.BaseURL,
		Year:          time.Now().Year(),
		Stats:         st,
		AppsCount:     len(s.idx.Current().PublicApps()),
		UploadEnabled: s.cfg.UploadEnabled,
	}
}

// formatBuildCommit drops the placeholder commit so the footer shows just the
// version rather than "dev+none".
func formatBuildCommit(raw string) string {
	switch strings.TrimSpace(raw) {
	case "", "none", "unknown":
		return ""
	}
	return strings.TrimSpace(raw)
}

// formatBuildTime turns the link-time stamp into something readable, in the
// server's local time zone. The stamp itself is stored as UTC so builds are
// comparable; only the display is localised.
//
// The offset is rendered numerically rather than as an abbreviation: "CST" alone
// is China Standard Time, Central Standard Time and Cuba Standard Time.
func formatBuildTime(raw string) string {
	raw = strings.TrimSpace(raw)
	switch raw {
	case "", "unknown", "none":
		// Nothing was injected. `docker compose up --build` cannot run `date`,
		// so its BUILD_DATE argument lands on a placeholder; fall back to the
		// timestamp of the binary itself, which in an image is when it was
		// linked. A cached layer therefore reports the build it really came
		// from, which is the honest answer.
		if t, ok := executableTime(); ok {
			return t.Local().Format("2006-01-02 15:04 -07:00")
		}
		return ""
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.Local().Format("2006-01-02 15:04 -07:00")
		}
	}
	return raw
}

// executableTime reports the modification time of the running binary.
func executableTime() (time.Time, bool) {
	path, err := os.Executable()
	if err != nil {
		return time.Time{}, false
	}
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() || st.ModTime().IsZero() {
		return time.Time{}, false
	}
	return st.ModTime(), true
}

// render executes a template and reports failures as a plain 500.
func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		// The header is already out; log and give up cleanly.
		log.Printf("template %s failed: %v", name, err)
	}
}

func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		s.writeError(w, r, http.StatusNotFound, "not_found", "no such endpoint: %s", r.URL.Path)
		return
	}
	http.NotFound(w, r)
}
