package server

import (
	"errors"
	"net/http"
	"path/filepath"

	"github.com/wuzhengmao/updatesite/internal/upload"
)

// handleDeleteRelease removes one published version.
//
// Destructive and irreversible, so it needs the application's upload token.
// Version "latest" is not special here: it is not a directory name, so the
// delete is refused rather than silently resolving to the newest release.
func (s *Server) handleDeleteRelease(w http.ResponseWriter, r *http.Request) {
	appID := r.PathValue("app")
	if !s.uploadsActive(w, r) {
		return
	}
	if !s.requireToken(w, r, appID) {
		return
	}
	version := r.PathValue("version")

	if err := upload.DeleteVersion(s.appsDir(), appID, version); err != nil {
		s.writeDeleteError(w, r, err)
		return
	}
	s.idx.Rescan()
	s.writeJSON(w, r, http.StatusOK, map[string]any{
		"ok":      true,
		"app":     appID,
		"version": version,
		"message": "版本 " + version + " 已删除",
	})
}

// handleDeleteApp removes an application entirely: every version, app.json and
// the icon.
func (s *Server) handleDeleteApp(w http.ResponseWriter, r *http.Request) {
	appID := r.PathValue("app")
	if !s.uploadsActive(w, r) {
		return
	}
	if !s.requireToken(w, r, appID) {
		return
	}

	if err := upload.DeleteApp(s.appsDir(), appID); err != nil {
		s.writeDeleteError(w, r, err)
		return
	}
	s.idx.Rescan()
	s.writeJSON(w, r, http.StatusOK, map[string]any{
		"ok":      true,
		"app":     appID,
		"message": "应用 " + appID + " 及其全部版本已删除",
	})
}

func (s *Server) writeDeleteError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, upload.ErrNotFound):
		s.writeError(w, r, http.StatusNotFound, "not_found", "%v", err)
	case upload.IsNotWritable(err):
		s.writeError(w, r, http.StatusServiceUnavailable, "not_writable", "%v", err)
	default:
		s.writeError(w, r, http.StatusBadRequest, "delete_failed", "%v", err)
	}
}

// appsDir is the archive root the uploads and deletions operate on.
func (s *Server) appsDir() string { return filepath.Join(s.cfg.DataDir, "apps") }
