package server

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/mti/updatesite/internal/token"
	"github.com/mti/updatesite/internal/upload"
)

// uploadResponse describes the outcome of a publish.
type uploadResponse struct {
	OK       bool          `json:"ok"`
	App      string        `json:"app"`
	Version  string        `json:"version"`
	Replaced bool          `json:"replaced"`
	Bytes    int64         `json:"bytes"`
	Files    []upload.File `json:"files"`
	PageURL  string        `json:"pageUrl"`
	APIURL   string        `json:"apiUrl"`
	Message  string        `json:"message"`
}

// handleUpload accepts a release archive and installs it.
//
// Two body shapes are accepted:
//
//	multipart/form-data   a file part holding the archive, plus an optional
//	                      "version" text field
//	anything else         the raw request body is the archive; the version then
//	                      comes from ?version=
//
// The archive is streamed straight to a temporary file inside the application
// directory, so an upload of any size costs a constant amount of memory.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.UploadEnabled {
		s.writeError(w, r, http.StatusForbidden, "uploads_disabled", "uploading is disabled on this site")
		return
	}
	appID := r.PathValue("app")
	if err := upload.ValidateAppID(appID); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_app", "%v", err)
		return
	}
	if !s.authorizeUpload(r, appID) {
		s.writeError(w, r, http.StatusUnauthorized, "unauthorized",
			"a valid upload token is required; an administrator can print it with: "+
				"UPLOAD_SECRET=<secret> updatesite token %s", appID)
		return
	}

	body := http.MaxBytesReader(w, r.Body, s.cfg.MaxUpload)
	defer body.Close()

	appsDir := filepath.Join(s.cfg.DataDir, "apps")
	version := strings.TrimSpace(r.URL.Query().Get("version"))
	filename := strings.TrimSpace(r.URL.Query().Get("filename"))

	var (
		spool   *os.File
		size    int64
		cleanup func()
	)
	defer func() {
		if cleanup != nil {
			cleanup()
		}
	}()

	spoolPart := func(name string, part io.Reader) error {
		f, n, done, err := upload.Spool(appsDir, appID, part, s.cfg.MaxUpload)
		if err != nil {
			return err
		}
		spool, size, cleanup = f, n, done
		if name != "" {
			filename = name
		}
		return nil
	}

	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType == "multipart/form-data" {
		mr, err := r.MultipartReader()
		if err != nil {
			s.writeError(w, r, http.StatusBadRequest, "bad_multipart", "%v", err)
			return
		}
		for {
			part, err := mr.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				s.uploadBodyError(w, r, err)
				return
			}
			switch {
			case part.FileName() != "":
				if spool != nil { // a second file part is ignored
					io.Copy(io.Discard, io.LimitReader(part, 1<<20))
					continue
				}
				// Spool consumes the part completely, so advancing to the next
				// part afterwards is safe.
				if err := spoolPart(part.FileName(), part); err != nil {
					s.uploadBodyError(w, r, err)
					return
				}
			case part.FormName() == "version":
				b, _ := io.ReadAll(io.LimitReader(part, 256))
				if v := strings.TrimSpace(string(b)); v != "" {
					version = v
				}
			default:
				io.Copy(io.Discard, io.LimitReader(part, 1<<16))
			}
		}
	} else if err := spoolPart("", body); err != nil {
		s.uploadBodyError(w, r, err)
		return
	}

	if spool == nil || size == 0 {
		s.writeError(w, r, http.StatusBadRequest, "no_file",
			"no archive was found in the request; send a multipart \"file\" part or the raw archive body")
		return
	}

	format, err := detectFormat(spool, filename)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "unsupported_archive", "%v", err)
		return
	}

	res, err := upload.Publish(spool, size, format, upload.Options{
		AppsDir: appsDir,
		AppID:   appID,
		Version: version,
		Limits:  upload.Limits{MaxTotalBytes: s.cfg.MaxUpload * 4, MaxEntries: 20000},
	})
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "publish_failed", "%v", err)
		return
	}

	// The scan runs in the background; the release shows up within a moment.
	s.idx.Rescan()

	s.writeJSON(w, r, http.StatusCreated, uploadResponse{
		OK:       true,
		App:      res.App,
		Version:  res.Version,
		Replaced: res.Replaced,
		Bytes:    res.Bytes,
		Files:    res.Files,
		PageURL:  s.absoluteURL(r, "/a/"+res.App+"/"+res.Version),
		APIURL:   s.absoluteURL(r, "/api/v1/apps/"+res.App+"/releases/"+res.Version),
		Message:  "release installed, the site is rescanning",
	})
}

// uploadBodyError turns a streaming failure into the right status code.
func (s *Server) uploadBodyError(w http.ResponseWriter, r *http.Request, err error) {
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge), errors.Is(err, upload.ErrTooLarge):
		s.writeError(w, r, http.StatusRequestEntityTooLarge, "too_large",
			"the upload exceeds the %d byte limit", s.cfg.MaxUpload)
	case upload.IsNotWritable(err):
		s.writeError(w, r, http.StatusServiceUnavailable, "not_writable", "%v", err)
	default:
		s.writeError(w, r, http.StatusBadRequest, "upload_failed", "%v", err)
	}
}

// authorizeUpload checks the upload token, accepting it either in the standard
// Authorization header or in X-Upload-Token.
func (s *Server) authorizeUpload(r *http.Request, appID string) bool {
	got := strings.TrimSpace(r.Header.Get("X-Upload-Token"))
	if got == "" {
		auth := strings.TrimSpace(r.Header.Get("Authorization"))
		if auth != "" {
			got = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(auth, "Bearer"), "bearer"))
		}
	}
	return got != "" && token.Matches(s.cfg.UploadSecret, appID, got)
}

// detectFormat identifies the archive, preferring the leading bytes over the
// file name because the name is caller supplied and easy to get wrong.
func detectFormat(f io.ReaderAt, filename string) (upload.Format, error) {
	head := make([]byte, 512)
	n, _ := f.ReadAt(head, 0)
	if format, ok := upload.Sniff(head[:n]); ok {
		return format, nil
	}
	if hint := upload.UnsupportedHint(filename); hint != "" {
		return "", errors.New(hint)
	}
	// Only tar lacks a reliable magic number, so only tar may be taken on the
	// strength of its name. A zip, gzip or bzip2 stream always starts with its
	// signature, so a mismatch means the payload really is something else.
	if format, ok := upload.FormatForName(filename); ok && format == upload.FormatTar {
		return format, nil
	}
	return "", errors.New("expected a zip, tar, tar.gz or tar.bz2 archive " +
		"(tar.xz, tar.zst, 7z and rar are not supported)")
}

// handleUploadPage renders the browser upload form.
func (s *Server) handleUploadPage(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.UploadEnabled {
		s.renderError(w, r, http.StatusNotFound, "本站未开启上传功能。")
		return
	}
	s.render(w, r, "upload", pageData{
		Site:  s.site(),
		Title: "上传发布",
	})
}
