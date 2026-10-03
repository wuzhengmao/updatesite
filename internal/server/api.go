package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/mti/updatesite/internal/buildinfo"
	"github.com/mti/updatesite/internal/index"
	"github.com/mti/updatesite/internal/semver"
)

// The API contract is expressed with explicit DTOs so the JSON field order is
// stable and every documented field is visible in one place.

type artifactDTO struct {
	File      string    `json:"file"`
	Name      string    `json:"name,omitempty"`
	OS        string    `json:"os,omitempty"`
	Arch      string    `json:"arch,omitempty"`
	Kind      string    `json:"kind,omitempty"`
	Size      int64     `json:"size"`
	SHA256    string    `json:"sha256,omitempty"`
	URL       string    `json:"url"`
	Path      string    `json:"path,omitempty"`
	Available bool      `json:"available"`
	Detected  bool      `json:"detected,omitempty"`
	Modified  time.Time `json:"modified,omitempty"`
}

type releaseDTO struct {
	App         string        `json:"app"`
	Version     string        `json:"version"`
	Channel     string        `json:"channel"`
	Title       string        `json:"title,omitempty"`
	Notes       string        `json:"notes,omitempty"`
	PublishedAt time.Time     `json:"publishedAt"`
	Prerelease  bool          `json:"prerelease"`
	Mandatory   bool          `json:"mandatory"`
	MinVersion  string        `json:"minVersion,omitempty"`
	Artifacts   []artifactDTO `json:"artifacts"`
	PageURL     string        `json:"pageUrl"`
}

type appDTO struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Summary      string            `json:"summary,omitempty"`
	Description  string            `json:"description,omitempty"`
	Homepage     string            `json:"homepage,omitempty"`
	Vendor       string            `json:"vendor,omitempty"`
	License      string            `json:"license,omitempty"`
	Tags         []string          `json:"tags,omitempty"`
	Platforms    []string          `json:"platforms,omitempty"`
	Channel      string            `json:"channel"`
	Channels     []string          `json:"channels"`
	IconURL      string            `json:"iconUrl,omitempty"`
	Latest       map[string]string `json:"latest"`
	ReleaseCount int               `json:"releaseCount"`
	UpdatedAt    time.Time         `json:"updatedAt"`
	PageURL      string            `json:"pageUrl"`
	APIURL       string            `json:"apiUrl"`
	Releases     []releaseDTO      `json:"releases,omitempty"`
}

type latestResponse struct {
	App      string       `json:"app"`
	Channel  string       `json:"channel"`
	Release  *releaseDTO  `json:"release,omitempty"`
	Download *artifactDTO `json:"download,omitempty"`
	Match    *matchInfo   `json:"match,omitempty"`
}

type matchInfo struct {
	OS        string        `json:"os,omitempty"`
	Arch      string        `json:"arch,omitempty"`
	Artifacts []artifactDTO `json:"artifacts"`
}

// checkResponse is what an installed client polls.
type checkResponse struct {
	App             string        `json:"app"`
	Name            string        `json:"name,omitempty"`
	CurrentVersion  string        `json:"currentVersion"`
	Channel         string        `json:"channel"`
	OS              string        `json:"os,omitempty"`
	Arch            string        `json:"arch,omitempty"`
	UpdateAvailable bool          `json:"updateAvailable"`
	UpToDate        bool          `json:"upToDate"`
	Mandatory       bool          `json:"mandatory"`
	LatestVersion   string        `json:"latestVersion,omitempty"`
	PublishedAt     *time.Time    `json:"publishedAt,omitempty"`
	MinVersion      string        `json:"minVersion,omitempty"`
	Notes           string        `json:"notes,omitempty"`
	Download        *artifactDTO  `json:"download,omitempty"`
	Artifacts       []artifactDTO `json:"artifacts,omitempty"`
	PageURL         string        `json:"pageUrl,omitempty"`
}

// ------------------------------------------------------------------ builders

func (s *Server) artifactDTO(r *http.Request, a *index.Artifact) artifactDTO {
	dto := artifactDTO{
		File: a.File, Name: a.Name, OS: a.OS, Arch: a.Arch, Kind: a.Kind,
		Size: a.Size, SHA256: a.SHA256, Path: a.Path,
		Available: a.Available, Detected: a.Detected, Modified: a.Modified,
	}
	if a.Path != "" {
		dto.URL = s.absoluteURL(r, a.Path)
	} else {
		dto.URL = a.External
	}
	return dto
}

func (s *Server) artifactDTOs(r *http.Request, arts []*index.Artifact) []artifactDTO {
	out := make([]artifactDTO, 0, len(arts))
	for _, a := range arts {
		out = append(out, s.artifactDTO(r, a))
	}
	return out
}

func (s *Server) releaseDTO(r *http.Request, app *index.App, rel *index.Release) releaseDTO {
	return releaseDTO{
		App: app.ID, Version: rel.Version, Channel: rel.Channel,
		Title: rel.Title, Notes: rel.Notes, PublishedAt: rel.PublishedAt,
		Prerelease: rel.Prerelease, Mandatory: rel.Mandatory, MinVersion: rel.MinVersion,
		Artifacts: s.artifactDTOs(r, rel.Artifacts),
		PageURL:   s.absoluteURL(r, "/a/"+app.ID+"/"+rel.Version),
	}
}

func (s *Server) appDTO(r *http.Request, app *index.App, withReleases bool) appDTO {
	dto := appDTO{
		ID: app.ID, Name: app.Name, Summary: app.Summary, Description: app.Description,
		Homepage: app.Homepage, Vendor: app.Vendor, License: app.License,
		Tags: app.Tags, Platforms: app.Platforms, Channel: app.Channel,
		Channels: app.Channels(), Latest: app.Latest, ReleaseCount: app.ReleaseCount,
		UpdatedAt: app.UpdatedAt,
		PageURL:   s.absoluteURL(r, "/a/"+app.ID),
		APIURL:    s.absoluteURL(r, "/api/v1/apps/"+app.ID),
	}
	if app.IconURL != "" {
		dto.IconURL = s.absoluteURL(r, app.IconURL)
	}
	if withReleases {
		dto.Releases = make([]releaseDTO, 0, len(app.Releases))
		for _, rel := range app.Releases {
			dto.Releases = append(dto.Releases, s.releaseDTO(r, app, rel))
		}
	}
	return dto
}

// bestArtifact resolves the artifact matching an os/arch pair and returns its
// DTO, or nil when the release ships nothing for that platform.
func (s *Server) bestArtifact(r *http.Request, rel *index.Release, osName, arch string) *artifactDTO {
	best := rel.Best(osName, arch)
	if best == nil {
		return nil
	}
	dto := s.artifactDTO(r, best)
	return &dto
}

// ---------------------------------------------------------------- endpoints

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, r, http.StatusOK, map[string]any{
		"status":        "ok",
		"version":       buildinfo.Version,
		"commit":        buildinfo.Commit,
		"builtAt":       buildinfo.Date,
		"time":          time.Now().UTC(),
		"uptimeSeconds": int(time.Since(s.started).Seconds()),
		"index":         s.idx.Stats(),
	})
}

func (s *Server) handleApps(w http.ResponseWriter, r *http.Request) {
	apps := s.idx.Current().PublicApps()
	withReleases := queryBool(r, "releases", false)
	out := make([]appDTO, 0, len(apps))
	for _, a := range apps {
		out = append(out, s.appDTO(r, a, withReleases))
	}
	s.writeJSON(w, r, http.StatusOK, map[string]any{"count": len(out), "apps": out})
}

func (s *Server) handleApp(w http.ResponseWriter, r *http.Request) {
	app, ok := s.lookupApp(w, r)
	if !ok {
		return
	}
	s.writeJSON(w, r, http.StatusOK, s.appDTO(r, app, queryBool(r, "releases", true)))
}

func (s *Server) handleReleases(w http.ResponseWriter, r *http.Request) {
	app, ok := s.lookupApp(w, r)
	if !ok {
		return
	}
	channel := index.NormalizeChannel(r.URL.Query().Get("channel"))
	wantPrerelease := queryBool(r, "prerelease", true)

	out := make([]releaseDTO, 0, len(app.Releases))
	for _, rel := range app.Releases {
		if channel != "" && rel.Channel != channel {
			continue
		}
		if !wantPrerelease && rel.Prerelease {
			continue
		}
		out = append(out, s.releaseDTO(r, app, rel))
	}
	s.writeJSON(w, r, http.StatusOK, map[string]any{
		"app": app.ID, "count": len(out), "releases": out,
	})
}

func (s *Server) handleRelease(w http.ResponseWriter, r *http.Request) {
	app, ok := s.lookupApp(w, r)
	if !ok {
		return
	}
	version := r.PathValue("version")
	rel, found := app.Release(version)
	if !found || rel == nil {
		s.writeError(w, r, http.StatusNotFound, "release_not_found",
			"application %q has no release %q", app.ID, version)
		return
	}
	s.writeJSON(w, r, http.StatusOK, s.releaseDTO(r, app, rel))
}

func (s *Server) handleLatest(w http.ResponseWriter, r *http.Request) {
	app, ok := s.lookupApp(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	osName := index.NormalizeOS(q.Get("os"))
	arch := index.NormalizeArch(q.Get("arch"))
	channel := index.NormalizeChannel(q.Get("channel"))

	rel := index.PickLatest(app, channel, queryBool(r, "prerelease", true))
	if rel == nil {
		s.writeError(w, r, http.StatusNotFound, "no_release",
			"application %q has no release in channel %q", app.ID, orDefault(channel, app.Channel))
		return
	}

	dto := s.releaseDTO(r, app, rel)
	out := latestResponse{App: app.ID, Channel: rel.Channel, Release: &dto}
	if osName != "" || arch != "" {
		out.Match = &matchInfo{OS: osName, Arch: arch, Artifacts: s.artifactDTOs(r, rel.Filter(osName, arch))}
	}
	out.Download = s.bestArtifact(r, rel, osName, arch)
	s.writeJSON(w, r, http.StatusOK, out)
}

func (s *Server) handleCheck(w http.ResponseWriter, r *http.Request) {
	app, ok := s.lookupApp(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	current := strings.TrimSpace(q.Get("version"))
	if current == "" {
		s.writeError(w, r, http.StatusBadRequest, "missing_version",
			"the version query parameter is required, e.g. ?version=1.2.3")
		return
	}
	osName := index.NormalizeOS(q.Get("os"))
	arch := index.NormalizeArch(q.Get("arch"))
	channel := index.NormalizeChannel(q.Get("channel"))

	resp := checkResponse{
		App: app.ID, Name: app.Name, CurrentVersion: current,
		Channel: orDefault(channel, app.Channel), OS: osName, Arch: arch,
		PageURL: s.absoluteURL(r, "/a/"+app.ID),
	}

	// Auto-update clients get stable releases unless they opt in: a channel
	// that only ships pre-releases reports "up to date" by default.
	rel := index.PickLatest(app, channel, queryBool(r, "prerelease", false))
	if rel == nil || semver.Compare(current, rel.Version) >= 0 {
		resp.UpToDate = true
		s.writeJSON(w, r, http.StatusOK, resp)
		return
	}

	resp.UpdateAvailable = true
	resp.LatestVersion = rel.Version
	resp.MinVersion = rel.MinVersion
	resp.Notes = rel.Notes
	resp.Mandatory = rel.Mandatory
	published := rel.PublishedAt
	resp.PublishedAt = &published
	resp.Artifacts = s.artifactDTOs(r, rel.Filter(osName, arch))
	resp.Download = s.bestArtifact(r, rel, osName, arch)
	s.writeJSON(w, r, http.StatusOK, resp)
}

func (s *Server) handleRescan(w http.ResponseWriter, r *http.Request) {
	if s.cfg.RescanToken != "" {
		got := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer"))
		if got != s.cfg.RescanToken {
			s.writeError(w, r, http.StatusUnauthorized, "unauthorized", "a valid rescan token is required")
			return
		}
	}
	s.idx.Rescan()
	s.writeJSON(w, r, http.StatusAccepted, map[string]any{"ok": true, "message": "rescan scheduled"})
}

// ------------------------------------------------------------------ helpers

func queryBool(r *http.Request, key string, def bool) bool {
	v, ok := r.URL.Query()[key]
	if !ok || len(v) == 0 || v[0] == "" {
		return def
	}
	switch strings.ToLower(v[0]) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
