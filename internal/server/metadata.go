package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/wuzhengmao/updatesite/internal/appmeta"
	"github.com/wuzhengmao/updatesite/internal/index"
)

// metadataResponse is what the edit form reads and writes.
type metadataResponse struct {
	App      string        `json:"app"`
	Metadata index.AppMeta `json:"metadata"`
	Icon     *iconInfo     `json:"icon,omitempty"`
	IconURL  string        `json:"iconUrl,omitempty"`
	Message  string        `json:"message,omitempty"`
}

type iconInfo struct {
	File string `json:"file"`
	URL  string `json:"url"`
}

// handleGetMetadata returns the raw app.json so the form can prefill. Unlike
// GET /api/v1/apps/{app} this is the stored file, not the merged view, so
// saving it back cannot freeze derived values such as the platform list.
func (s *Server) handleGetMetadata(w http.ResponseWriter, r *http.Request) {
	appID := r.PathValue("app")
	// The token also proves the caller knows which application they are
	// editing, so require it for symmetry with the write.
	if !s.requireToken(w, r, appID) {
		return
	}

	// Read validates the application id before touching the filesystem.
	meta, err := appmeta.Read(filepath.Join(s.cfg.DataDir, "apps"), appID)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "read_failed", "%v", err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, s.metadataPayload(r, appID, meta))
}

// handlePutMetadata stores app.json.
func (s *Server) handlePutMetadata(w http.ResponseWriter, r *http.Request) {
	appID := r.PathValue("app")
	if !s.uploadsActive(w, r) {
		return
	}
	if !s.requireToken(w, r, appID) {
		return
	}

	body := http.MaxBytesReader(w, r.Body, 1<<20)
	defer body.Close()

	var meta index.AppMeta
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&meta); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_json",
			"应用信息不是合法的 JSON：%v", err)
		return
	}

	appsDir := filepath.Join(s.cfg.DataDir, "apps")
	if err := appmeta.Write(appsDir, appID, meta); err != nil {
		s.writeMetadataError(w, r, err)
		return
	}

	s.idx.Rescan()
	stored, _ := appmeta.Read(appsDir, appID)
	payload := s.metadataPayload(r, appID, stored)
	payload.Message = "已保存，站点正在重新扫描"
	s.writeJSON(w, r, http.StatusOK, payload)
}

// handlePutIcon stores an uploaded icon and points app.json at it.
func (s *Server) handlePutIcon(w http.ResponseWriter, r *http.Request) {
	appID := r.PathValue("app")
	if !s.uploadsActive(w, r) {
		return
	}
	if !s.requireToken(w, r, appID) {
		return
	}

	body := http.MaxBytesReader(w, r.Body, appmeta.MaxIconBytes+1024)
	defer body.Close()

	data, err := io.ReadAll(body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			s.writeError(w, r, http.StatusRequestEntityTooLarge, "too_large",
				"图标不能超过 %d KB", appmeta.MaxIconBytes/1024)
			return
		}
		s.writeError(w, r, http.StatusBadRequest, "read_failed", "%v", err)
		return
	}

	appsDir := filepath.Join(s.cfg.DataDir, "apps")
	name, err := appmeta.WriteIcon(appsDir, appID, data)
	if err != nil {
		s.writeMetadataError(w, r, err)
		return
	}

	s.idx.Rescan()
	stored, _ := appmeta.Read(appsDir, appID)
	payload := s.metadataPayload(r, appID, stored)
	payload.Message = "图标已更新为 " + name + "，站点正在重新扫描"
	s.writeJSON(w, r, http.StatusOK, payload)
}

// requireToken checks the upload token of an application, writing the refusal
// when it does not match.
func (s *Server) requireToken(w http.ResponseWriter, r *http.Request, appID string) bool {
	if s.authorizeUpload(r, appID) {
		return true
	}
	s.writeError(w, r, http.StatusUnauthorized, "unauthorized",
		"a valid upload token is required; an administrator can print it with: "+
			"UPLOAD_SECRET=<secret> updatesite token %s", appID)
	return false
}

// uploadsActive reports whether the upload feature is on at all, writing the
// refusal if it is not.
func (s *Server) uploadsActive(w http.ResponseWriter, r *http.Request) bool {
	if s.cfg.UploadEnabled {
		return true
	}
	s.writeError(w, r, http.StatusForbidden, "uploads_disabled",
		"this site has uploads turned off (no UPLOAD_SECRET is configured)")
	return false
}

func (s *Server) writeMetadataError(w http.ResponseWriter, r *http.Request, err error) {
	if strings.Contains(err.Error(), "mounted read-write") {
		s.writeError(w, r, http.StatusServiceUnavailable, "not_writable", "%v", err)
		return
	}
	s.writeError(w, r, http.StatusBadRequest, "save_failed", "%v", err)
}

// metadataPayload assembles the response, including where the icon lives now.
func (s *Server) metadataPayload(r *http.Request, appID string, meta index.AppMeta) metadataResponse {
	out := metadataResponse{App: appID, Metadata: meta}
	if meta.Icon != "" {
		out.Icon = &iconInfo{File: meta.Icon, URL: s.absoluteURL(r, "/a/"+appID+"/icon")}
		out.IconURL = out.Icon.URL
	}
	return out
}
