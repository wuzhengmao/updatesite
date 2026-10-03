package server

import (
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/mti/updatesite/internal/index"
)

// installers maps extensions to the media type a client should expect. Anything
// unknown is served as a generic binary.
var installers = map[string]string{
	".exe": "application/vnd.microsoft.portable-executable",
	".msi": "application/x-msi", ".msix": "application/msix",
	".dmg": "application/x-apple-diskimage", ".pkg": "application/vnd.apple.installer+xml",
	".apk": "application/vnd.android.package-archive",
	".aab": "application/octet-stream", ".ipa": "application/octet-stream",
	".deb": "application/vnd.debian.binary-package", ".rpm": "application/x-rpm",
	".appimage": "application/x-executable", ".app": "application/x-apple-diskimage",
	".zip": "application/zip", ".7z": "application/x-7z-compressed",
	".tar": "application/x-tar", ".gz": "application/gzip",
	".tgz": "application/gzip", ".xz": "application/x-xz", ".zst": "application/zstd",
	".iso": "application/x-iso9660-image",
	".jar": "application/java-archive",
}

// handleDownload serves an artifact. Version "latest" resolves to the newest
// release, and file names may be given as a bare base name.
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	appID := r.PathValue("app")
	version := r.PathValue("version")
	file := r.PathValue("file")

	app, ok := s.idx.Current().App(appID)
	if !ok || app.Hidden {
		s.writeError(w, r, http.StatusNotFound, "app_not_found", "no application named %q", appID)
		return
	}
	rel, found := app.Release(version)
	if !found || rel == nil {
		s.writeError(w, r, http.StatusNotFound, "release_not_found",
			"application %q has no release %q", appID, version)
		return
	}

	art := resolveArtifact(rel, file)
	if art == nil {
		s.writeError(w, r, http.StatusNotFound, "artifact_not_found",
			"release %s of %s has no artifact %q", rel.Version, appID, file)
		return
	}

	if art.Path == "" {
		if art.External != "" {
			http.Redirect(w, r, art.External, http.StatusFound)
			return
		}
		s.writeError(w, r, http.StatusNotFound, "artifact_unavailable",
			"artifact %q is not available for download", art.File)
		return
	}

	abs := filepath.Join(rel.Dir, filepath.FromSlash(art.File))
	if !within(rel.Dir, abs) {
		s.writeError(w, r, http.StatusForbidden, "forbidden", "path escapes the release directory")
		return
	}

	f, err := os.Open(abs)
	if err != nil {
		s.writeError(w, r, http.StatusNotFound, "artifact_unavailable",
			"artifact %q cannot be read: %v", art.File, err)
		return
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil || st.IsDir() {
		s.writeError(w, r, http.StatusNotFound, "artifact_unavailable", "artifact %q is not a file", art.File)
		return
	}

	// Media type and saved file name come from the real file, never from the
	// display name, which may be a human label without an extension.
	filename := filepath.Base(art.File)

	h := w.Header()
	h.Set("Content-Type", contentType(filename))
	h.Set("Content-Disposition", contentDisposition(filename))
	h.Set("Cache-Control", "public, max-age=3600")

	if art.SHA256 != "" {
		etag := `"sha256-` + art.SHA256 + `"`
		h.Set("ETag", etag)
		h.Set("X-Checksum-Sha256", art.SHA256)
		if etagMatches(r.Header.Get("If-None-Match"), etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}

	// ServeContent adds Range, If-Range and If-Modified-Since handling.
	http.ServeContent(w, r, filename, st.ModTime(), f)
}

// resolveArtifact finds an artifact by its relative path, falling back to a
// case-insensitive match and then to a base name match.
func resolveArtifact(rel *index.Release, file string) *index.Artifact {
	if a := rel.Artifact(file); a != nil {
		return a
	}
	for _, a := range rel.Artifacts {
		if strings.EqualFold(a.File, file) {
			return a
		}
	}
	want := filepath.Base(file)
	for _, a := range rel.Artifacts {
		if strings.EqualFold(filepath.Base(a.File), want) {
			return a
		}
	}
	return nil
}

// within reports whether path stays inside dir.
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func contentType(filename string) string {
	ext := index.Extension(filename)
	if ct, ok := installers[ext]; ok {
		return ct
	}
	if ct := mime.TypeByExtension(ext); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

// contentDisposition builds an RFC 6266 header carrying both an ASCII fallback
// and the UTF-8 name.
func contentDisposition(name string) string {
	if name == "" {
		name = "download"
	}
	ascii := make([]rune, 0, len(name))
	for _, r := range name {
		switch {
		case r > 127 || r == '"' || r == '\\':
			ascii = append(ascii, '_')
		default:
			ascii = append(ascii, r)
		}
	}
	return `attachment; filename="` + string(ascii) + `"; filename*=UTF-8''` + urlEscape(name)
}

// etagMatches implements the If-None-Match comparison for a single entity tag.
func etagMatches(header, etag string) bool {
	header = strings.TrimSpace(header)
	if header == "" {
		return false
	}
	if header == "*" {
		return true
	}
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		candidate = strings.TrimPrefix(candidate, "W/")
		if candidate == etag {
			return true
		}
	}
	return false
}

func urlEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' ||
			c == '-' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)
			continue
		}
		const hex = "0123456789ABCDEF"
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0x0f])
	}
	return b.String()
}
