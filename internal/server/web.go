package server

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mti/updatesite/internal/index"
)

// pageData carries everything the templates may use. One struct keeps the
// template files free of type gymnastics.
type pageData struct {
	Site      siteInfo
	Title     string
	Apps      []*index.App
	App       *index.App
	Release   *index.Release
	IsLatest  bool
	Notes     template.HTML
	About     template.HTML
	ErrorCode int
	ErrorText string
}

func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"md":        RenderMarkdown,
		"size":      humanSize,
		"date":      func(t time.Time) string { return t.Format("2006-01-02") },
		"datetime":  func(t time.Time) string { return t.Format("2006-01-02 15:04") },
		"ago":       humanAgo,
		"shortsha":  shortSHA,
		"osLabel":   osLabel,
		"archLabel": archLabel,
		"kindLabel": kindLabel,
		"lower":     strings.ToLower,
		"initial":   initial,
	}
}

// initial returns the first rune of a name, used by the icon placeholder.
func initial(s string) string {
	for _, r := range strings.TrimSpace(s) {
		return string(r)
	}
	return "?"
}

func (s *Server) handleIndexPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "index", pageData{
		Site:  s.site(),
		Title: s.cfg.SiteTitle,
		Apps:  s.idx.Current().PublicApps(),
	})
}

func (s *Server) handleAppPage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("app")
	app, ok := s.idx.Current().App(id)
	if !ok || app.Hidden {
		s.renderError(w, r, http.StatusNotFound, fmt.Sprintf("找不到应用 %q。", id))
		return
	}

	version := r.PathValue("version")
	var rel *index.Release
	isLatest := version == "" || version == "latest"
	if isLatest {
		rel = index.PickLatest(app, "", true)
	} else {
		rel, _ = app.Release(version)
		if rel == nil {
			s.renderError(w, r, http.StatusNotFound,
				fmt.Sprintf("应用 %s 没有版本 %q。", app.Name, version))
			return
		}
	}

	data := pageData{
		Site:     s.site(),
		Title:    app.Name,
		App:      app,
		Release:  rel,
		IsLatest: isLatest,
		About:    RenderMarkdown(app.Description),
	}
	if rel != nil {
		data.Title = app.Name + " " + rel.Version
		data.Notes = RenderMarkdown(rel.Notes)
	}
	s.render(w, r, "app", data)
}

func (s *Server) handleIcon(w http.ResponseWriter, r *http.Request) {
	app, ok := s.idx.Current().App(r.PathValue("app"))
	if !ok || app.Hidden || app.IconFile() == "" {
		http.NotFound(w, r)
		return
	}
	abs := filepath.Join(app.Dir, filepath.FromSlash(app.IconFile()))
	if !within(app.Dir, abs) {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(abs)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	http.ServeContent(w, r, filepath.Base(abs), st.ModTime(), f)
}

func (s *Server) renderError(w http.ResponseWriter, r *http.Request, code int, message string) {
	if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/dl/") {
		s.writeError(w, r, code, http.StatusText(code), "%s", message)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(code)
	if err := s.tmpl.ExecuteTemplate(w, "error", pageData{
		Site:      s.site(),
		Title:     http.StatusText(code),
		ErrorCode: code,
		ErrorText: message,
	}); err != nil {
		log.Printf("template error failed: %v", err)
	}
}

// ------------------------------------------------------------------ helpers

func humanSize(n int64) string {
	if n <= 0 {
		return "-"
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	i := 0
	v := float64(n)
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d B", n)
	}
	digits := 1
	if v >= 100 {
		digits = 0
	}
	return fmt.Sprintf("%.*f %s", digits, v, units[i])
}

func humanAgo(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < 0:
		return t.Format("2006-01-02")
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%d 天前", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}

func shortSHA(s string) string {
	if len(s) <= 16 {
		return s
	}
	return s[:16]
}

func osLabel(os string) string {
	switch os {
	case index.OSWindows:
		return "Windows"
	case index.OSLinux:
		return "Linux"
	case index.OSMacOS:
		return "macOS"
	case index.OSAndroid:
		return "Android"
	case index.OSIOS:
		return "iOS"
	case index.OSWeb:
		return "Web"
	case "":
		return "通用"
	}
	return os
}

func archLabel(arch string) string {
	switch arch {
	case index.ArchX64:
		return "x86 64 位"
	case index.ArchX86:
		return "x86 32 位"
	case index.ArchARM64:
		return "ARM 64 位"
	case index.ArchARM32:
		return "ARM 32 位"
	case index.ArchUniversal:
		return "通用"
	case "":
		return "-"
	}
	return arch
}

func kindLabel(kind string) string {
	switch kind {
	case "installer":
		return "安装包"
	case "portable":
		return "便携版"
	case "archive":
		return "压缩包"
	case "package":
		return "软件包"
	case "image":
		return "镜像"
	case "":
		return "-"
	}
	return "其他"
}
