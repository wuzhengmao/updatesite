package server_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mti/updatesite/internal/config"
	"github.com/mti/updatesite/internal/index"
	"github.com/mti/updatesite/internal/server"
	"github.com/mti/updatesite/internal/token"
)

// uploadSecret is the deployment secret the upload tests mint tokens from.
const uploadSecret = "test-upload-secret-not-a-real-one"

// zipBytes builds a zip archive in memory for upload tests.
func zipBytes(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// postMultipart sends a multipart upload with an optional bearer token.
func postMultipart(t *testing.T, url string, archive []byte, fields map[string]string, bearer string) (int, map[string]any) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "release.zip")
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(archive)
	for k, v := range fields {
		mw.WriteField(k, v)
	}
	mw.Close()

	req, err := http.NewRequest(http.MethodPost, url, &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestUploadRequiresToken(t *testing.T) {
	ts, _ := newSite(t)
	archive := zipBytes(t, map[string]string{"App-1.0.0-windows-x64.exe": "win"})
	url := ts.URL + "/api/v1/apps/newapp/upload"

	if code, _ := postMultipart(t, url, archive, map[string]string{"version": "1.0.0"}, ""); code != http.StatusUnauthorized {
		t.Errorf("without a token: status %d, want 401", code)
	}
	if code, _ := postMultipart(t, url, archive, map[string]string{"version": "1.0.0"}, "not-the-token"); code != http.StatusUnauthorized {
		t.Errorf("with a wrong token: status %d, want 401", code)
	}
	// A token minted for a different application must not work either.
	if code, _ := postMultipart(t, url, archive, map[string]string{"version": "1.0.0"}, token.For(uploadSecret, "otherapp")); code != http.StatusUnauthorized {
		t.Errorf("with another app's token: status %d, want 401", code)
	}
}

func TestUploadPublishesRelease(t *testing.T) {
	ts, dir := newSite(t)
	appID := "newapp"

	code, body := postMultipart(t, ts.URL+"/api/v1/apps/"+appID+"/upload", zipBytes(t, map[string]string{
		"App-1.0.0-windows-x64.exe":    "windows build",
		"App-1.0.0-linux-arm64.tar.gz": "linux build",
		"CHANGELOG.md":                 "## 1.0.0\n\n- 首个版本",
		"release.json":                 `{"channel":"stable","title":"首发"}`,
	}), map[string]string{"version": "1.0.0"}, token.For(uploadSecret, appID))

	if code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %v", code, body)
	}
	if body["version"] != "1.0.0" || body["replaced"] != false {
		t.Errorf("unexpected response: %v", body)
	}

	// The files must be on disk, under the version directory.
	for _, name := range []string{"App-1.0.0-windows-x64.exe", "CHANGELOG.md", "release.json"} {
		p := filepath.Join(dir, "apps", appID, "1.0.0", name)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
	// Nothing temporary may be left behind.
	entries, _ := os.ReadDir(filepath.Join(dir, "apps", appID))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Errorf("leftover staging entry %q", e.Name())
		}
	}

	// The release becomes visible once the background scan lands.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_, api := getJSON(t, ts, "/api/v1/apps/"+appID+"/latest")
		if rel, ok := api["release"].(map[string]any); ok && rel["version"] == "1.0.0" {
			if rel["title"] != "首发" {
				t.Errorf("title = %v", rel["title"])
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("the uploaded release never appeared in the API")
}

func TestUploadRawBody(t *testing.T) {
	ts, dir := newSite(t)
	appID := "rawapp"
	archive := zipBytes(t, map[string]string{"Tool-2.0.0-linux-x64.tar.gz": "linux"})

	req, _ := http.NewRequest(http.MethodPost,
		ts.URL+"/api/v1/apps/"+appID+"/upload?version=2.0.0&filename=release.zip",
		bytes.NewReader(archive))
	req.Header.Set("Content-Type", "application/zip")
	req.Header.Set("X-Upload-Token", token.For(uploadSecret, appID))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(dir, "apps", appID, "2.0.0", "Tool-2.0.0-linux-x64.tar.gz")); err != nil {
		t.Errorf("raw upload did not land on disk: %v", err)
	}
}

func TestUploadInfersVersionFromDirectory(t *testing.T) {
	ts, dir := newSite(t)
	appID := "inferapp"

	code, body := postMultipart(t, ts.URL+"/api/v1/apps/"+appID+"/upload", zipBytes(t, map[string]string{
		"myapp-3.2.1/App-3.2.1-windows-x64.exe": "win",
		"myapp-3.2.1/CHANGELOG.md":              "notes",
	}), nil, token.For(uploadSecret, appID))
	if code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %v", code, body)
	}
	if body["version"] != "3.2.1" {
		t.Errorf("version = %v, want 3.2.1", body["version"])
	}
	if _, err := os.Stat(filepath.Join(dir, "apps", appID, "3.2.1", "App-3.2.1-windows-x64.exe")); err != nil {
		t.Errorf("release not installed: %v", err)
	}
}

func TestUploadRejectsBadArchives(t *testing.T) {
	ts, _ := newSite(t)
	appID := "badapp"
	url := ts.URL + "/api/v1/apps/" + appID + "/upload"
	tok := token.For(uploadSecret, appID)

	cases := []struct {
		name  string
		body  []byte
		field map[string]string
		want  string
	}{
		{"not an archive", []byte("hello world"), map[string]string{"version": "1.0.0"}, "unsupported_archive"},
		{"no installable files", zipBytes(t, map[string]string{"CHANGELOG.md": "x"}), map[string]string{"version": "1.0.0"}, "publish_failed"},
		{"undeterminable version", zipBytes(t, map[string]string{"a.exe": "x"}), nil, "publish_failed"},
		{"invalid version", zipBytes(t, map[string]string{"a.exe": "x"}), map[string]string{"version": "../x"}, "publish_failed"},
	}
	for _, c := range cases {
		code, body := postMultipart(t, url, c.body, c.field, tok)
		if code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400 (%v)", c.name, code, body)
			continue
		}
		errObj, _ := body["error"].(map[string]any)
		if errObj["code"] != c.want {
			t.Errorf("%s: error code = %v, want %q", c.name, errObj["code"], c.want)
		}
	}
}

func TestUploadDisabled(t *testing.T) {
	ts, dir := newSite(t)
	// Rebuild a server with uploads turned off.
	cfg := config.Config{
		DataDir: dir, CacheDir: filepath.Join(dir, "cache"),
		ScanInterval: time.Minute, SiteTitle: "T", CORSOrigin: "*",
		UploadEnabled: false, UploadSecret: uploadSecret, MaxUpload: 1 << 20,
	}
	idx := index.New(cfg.DataDir, cfg.CacheDir)
	idx.Scan()
	srv, err := server.New(cfg, idx)
	if err != nil {
		t.Fatal(err)
	}
	off := httptest.NewServer(srv.Handler())
	defer off.Close()

	archive := zipBytes(t, map[string]string{"App-1.0.0-windows-x64.exe": "win"})
	code, _ := postMultipart(t, off.URL+"/api/v1/apps/disabledapp/upload", archive,
		map[string]string{"version": "1.0.0"}, token.For(uploadSecret, "disabledapp"))
	if code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 when uploads are disabled", code)
	}
	if code, _ := getHTML(t, off, "/upload"); code != http.StatusNotFound {
		t.Errorf("/upload status = %d, want 404 when uploads are disabled", code)
	}
	_ = ts
}

func TestUploadPage(t *testing.T) {
	ts, _ := newSite(t)
	code, html := getHTML(t, ts, "/upload")
	if code != 200 {
		t.Fatalf("/upload status = %d", code)
	}
	for _, want := range []string{"上传发布", "upload-form", "/static/upload.js"} {
		if !strings.Contains(html, want) {
			t.Errorf("/upload is missing %q", want)
		}
	}
}

// write creates a file below root, making any missing parent directories.
func write(t *testing.T, root, rel, content string) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// newSite builds a throwaway archive and returns a live test server.
func newSite(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()

	write(t, dir, "apps/demo/app.json", `{
		"name": "Demo App",
		"summary": "A demonstration application",
		"vendor": "MTI",
		"tags": ["desktop"]
	}`)

	// 1.0.0 - auto detected artifacts plus a sidecar checksum.
	write(t, dir, "apps/demo/1.0.0/Demo-1.0.0-windows-x64.exe", "old windows build")
	write(t, dir, "apps/demo/1.0.0/Demo-1.0.0-linux-amd64.tar.gz", "old linux build")
	write(t, dir, "apps/demo/1.0.0/CHANGELOG.md", "## 1.0.0\n\n- first release")

	// 1.2.0 - declared metadata and an explicit checksum.
	body := "new windows build"
	sum := sha256.Sum256([]byte(body))
	write(t, dir, "apps/demo/1.2.0/Demo-1.2.0-windows-x64.exe", body)
	write(t, dir, "apps/demo/1.2.0/Demo-1.2.0-windows-x64.exe.sha256", hex.EncodeToString(sum[:]))
	write(t, dir, "apps/demo/1.2.0/Demo-1.2.0-macos-universal.dmg", "mac build")
	write(t, dir, "apps/demo/1.2.0/release.json", `{
		"channel": "stable",
		"title": "1.2.0 正式版",
		"mandatory": true,
		"artifacts": [
			{"file": "Demo-1.2.0-macos-universal.dmg", "kind": "installer", "os": "macos", "arch": "universal"}
		]
	}`)

	// A second app whose only release is a pre-release.
	write(t, dir, "apps/edge/2.0.0-rc.1/edge-2.0.0-rc.1-windows-x64.exe", "rc build")

	// Distractors that must be ignored.
	write(t, dir, "apps/demo/notes.txt", "not a version directory")
	write(t, dir, "apps/demo/.hidden/1.0.0/x.exe", "hidden")

	cfg := config.Config{
		DataDir:       dir,
		CacheDir:      filepath.Join(dir, "cache"),
		ScanInterval:  time.Minute,
		SiteTitle:     "Test Update Site",
		CORSOrigin:    "*",
		UploadEnabled: true,
		UploadSecret:  uploadSecret,
		MaxUpload:     8 << 20,
	}
	idx := index.New(cfg.DataDir, cfg.CacheDir)
	idx.Scan()

	// Run keeps the rescan channel drained so POST /api/v1/rescan is exercised.
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go idx.Run(ctx, cfg.ScanInterval)

	srv, err := server.New(cfg, idx)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, dir
}

// getJSON fetches a path and decodes it into a generic map.
func getJSON(t *testing.T, ts *httptest.Server, path string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("GET %s: decoding response: %v", path, err)
	}
	return resp.StatusCode, out
}

func TestListApps(t *testing.T) {
	ts, _ := newSite(t)

	code, body := getJSON(t, ts, "/api/v1/apps")
	if code != 200 {
		t.Fatalf("status = %d, want 200", code)
	}
	apps, _ := body["apps"].([]any)
	if len(apps) != 2 {
		t.Fatalf("got %d apps, want 2: %v", len(apps), body)
	}
	first := apps[0].(map[string]any)
	if first["id"] != "demo" {
		t.Errorf("first app = %v, want demo (names sort alphabetically)", first["id"])
	}
	if first["name"] != "Demo App" {
		t.Errorf("name = %v, want %q", first["name"], "Demo App")
	}
	if latest, _ := first["latest"].(map[string]any); latest["stable"] != "1.2.0" {
		t.Errorf("latest.stable = %v, want 1.2.0", latest["stable"])
	}
}

func TestLatestPicksHighestVersion(t *testing.T) {
	ts, _ := newSite(t)

	code, body := getJSON(t, ts, "/api/v1/apps/demo/latest")
	if code != 200 {
		t.Fatalf("status = %d, want 200", code)
	}
	rel := body["release"].(map[string]any)
	if rel["version"] != "1.2.0" {
		t.Errorf("version = %v, want 1.2.0", rel["version"])
	}
	if rel["mandatory"] != true {
		t.Errorf("mandatory = %v, want true", rel["mandatory"])
	}
	if rel["title"] != "1.2.0 正式版" {
		t.Errorf("title = %v", rel["title"])
	}
	if !strings.Contains(rel["pageUrl"].(string), "/a/demo/1.2.0") {
		t.Errorf("pageUrl = %v", rel["pageUrl"])
	}
}

func TestLatestMatchesPlatform(t *testing.T) {
	ts, _ := newSite(t)

	_, body := getJSON(t, ts, "/api/v1/apps/demo/latest?os=windows&arch=x64")
	dl, ok := body["download"].(map[string]any)
	if !ok {
		t.Fatalf("no download in %v", body)
	}
	if dl["file"] != "Demo-1.2.0-windows-x64.exe" {
		t.Errorf("file = %v", dl["file"])
	}
	want := sha256.Sum256([]byte("new windows build"))
	if dl["sha256"] != hex.EncodeToString(want[:]) {
		t.Errorf("sha256 = %v, want the sidecar value", dl["sha256"])
	}
	url, _ := dl["url"].(string)
	if !strings.HasSuffix(url, "/dl/demo/1.2.0/Demo-1.2.0-windows-x64.exe") {
		t.Errorf("url = %v", url)
	}
}

func TestCheckUpdate(t *testing.T) {
	ts, _ := newSite(t)

	code, body := getJSON(t, ts, "/api/v1/apps/demo/check?version=1.0.0&os=windows&arch=x64")
	if code != 200 {
		t.Fatalf("status = %d, want 200", code)
	}
	if body["updateAvailable"] != true {
		t.Errorf("updateAvailable = %v, want true", body["updateAvailable"])
	}
	if body["latestVersion"] != "1.2.0" {
		t.Errorf("latestVersion = %v", body["latestVersion"])
	}
	if body["upToDate"] != false {
		t.Errorf("upToDate = %v, want false", body["upToDate"])
	}

	// The installed version is already the newest.
	_, body = getJSON(t, ts, "/api/v1/apps/demo/check?version=1.2.0&os=windows&arch=x64")
	if body["upToDate"] != true || body["updateAvailable"] != false {
		t.Errorf("up-to-date check = %v", body)
	}
}

func TestCheckRequiresVersion(t *testing.T) {
	ts, _ := newSite(t)
	if code, _ := getJSON(t, ts, "/api/v1/apps/demo/check"); code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", code)
	}
}

// An app whose only release is a pre-release reports "up to date" to clients
// that did not opt in, but /latest still shows it.
func TestPrereleaseHandling(t *testing.T) {
	ts, _ := newSite(t)

	_, body := getJSON(t, ts, "/api/v1/apps/edge/check?version=1.0.0")
	if body["upToDate"] != true || body["updateAvailable"] != false {
		t.Errorf("prerelease-only check = %v, want up to date", body)
	}

	_, body = getJSON(t, ts, "/api/v1/apps/edge/check?version=1.0.0&prerelease=true")
	if body["latestVersion"] != "2.0.0-rc.1" {
		t.Errorf("latestVersion = %v, want 2.0.0-rc.1", body["latestVersion"])
	}

	_, body = getJSON(t, ts, "/api/v1/apps/edge/latest")
	if rel := body["release"].(map[string]any); rel["prerelease"] != true {
		t.Errorf("latest prerelease flag = %v, want true", rel["prerelease"])
	}
}

func TestReleaseFallbackMatching(t *testing.T) {
	ts, _ := newSite(t)

	// A mac client with an unspecified architecture still resolves: the
	// universal build is the only candidate.
	_, body := getJSON(t, ts, "/api/v1/apps/demo/latest?os=macos")
	if dl := body["download"].(map[string]any); dl["arch"] != "universal" {
		t.Errorf("arch = %v, want universal", dl["arch"])
	}

	// An unknown platform yields no match, not an error.
	code, body := getJSON(t, ts, "/api/v1/apps/demo/latest?os=linux")
	if code != 200 {
		t.Fatalf("status = %d, want 200", code)
	}
	if body["download"] != nil {
		t.Errorf("download = %v, want nil for a platform with no artifacts", body["download"])
	}
}

func TestDownload(t *testing.T) {
	ts, _ := newSite(t)

	for _, path := range []string{
		"/dl/demo/1.2.0/Demo-1.2.0-windows-x64.exe",
		"/dl/demo/latest/Demo-1.2.0-windows-x64.exe",
		"/dl/demo/1.2.0/Demo-1.2.0-windows-x64.exe", // exact repeat, exercises ETag
	} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body := make([]byte, 64)
		n, _ := resp.Body.Read(body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("GET %s: status %d", path, resp.StatusCode)
		}
		if got := string(body[:n]); got != "new windows build" {
			t.Errorf("GET %s: body = %q", path, got)
		}
		if cd := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
			t.Errorf("GET %s: Content-Disposition = %q", path, cd)
		}
	}

	// A bare base name resolves too.
	resp, err := http.Get(ts.URL + "/dl/demo/1.2.0/Demo-1.2.0-macos-universal.dmg")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("dmg download status = %d", resp.StatusCode)
	}
}

func TestDownloadRange(t *testing.T) {
	ts, _ := newSite(t)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/dl/demo/1.2.0/Demo-1.2.0-windows-x64.exe", nil)
	req.Header.Set("Range", "bytes=0-2")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", resp.StatusCode)
	}
	body := make([]byte, 8)
	n, _ := resp.Body.Read(body)
	if got := string(body[:n]); got != "new" {
		t.Errorf("partial body = %q, want %q", got, "new")
	}
}

func TestDownloadNotFound(t *testing.T) {
	ts, _ := newSite(t)
	resp, err := http.Get(ts.URL + "/dl/demo/1.2.0/missing.exe")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestPagesRender(t *testing.T) {
	ts, _ := newSite(t)

	for _, path := range []string{"/", "/a/demo", "/a/demo/1.0.0"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 1<<16)
		n, _ := resp.Body.Read(buf)
		resp.Body.Close()
		html := string(buf[:n])
		if resp.StatusCode != 200 {
			t.Fatalf("GET %s: status %d", path, resp.StatusCode)
		}
		if !strings.Contains(html, "Demo App") {
			t.Errorf("GET %s: page does not mention the app name", path)
		}
	}

	resp, err := http.Get(ts.URL + "/a/nope")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown app status = %d, want 404", resp.StatusCode)
	}
}

func TestChangelogRendered(t *testing.T) {
	ts, _ := newSite(t)
	resp, err := http.Get(ts.URL + "/a/demo/1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1<<16)
	n, _ := resp.Body.Read(buf)
	resp.Body.Close()
	if html := string(buf[:n]); !strings.Contains(html, "<h2>1.0.0</h2>") {
		t.Errorf("changelog heading was not rendered as markdown")
	}
}

func TestRescanEndpoint(t *testing.T) {
	ts, dir := newSite(t)

	write(t, dir, "apps/demo/1.3.0/Demo-1.3.0-windows-x64.exe", "even newer")

	resp, err := http.Post(ts.URL+"/api/v1/rescan", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("rescan status = %d, want 202", resp.StatusCode)
	}

	// The handler schedules the scan; poll briefly for it to land.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_, body := getJSON(t, ts, "/api/v1/apps/demo/latest")
		if rel := body["release"].(map[string]any); rel["version"] == "1.3.0" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("rescan did not publish the new release")
}

// getHTML fetches a page and returns its status and body.
func getHTML(t *testing.T, ts *httptest.Server, path string) (int, string) {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Get(ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}

func TestDocsPages(t *testing.T) {
	ts, _ := newSite(t)

	// /docs serves the first document, which is the release specification.
	code, html := getHTML(t, ts, "/docs")
	if code != 200 {
		t.Fatalf("GET /docs: status %d", code)
	}
	if !strings.Contains(html, "应用发布规范") {
		t.Errorf("the release spec title is missing from /docs")
	}
	// The spec is table-heavy, so this also proves pipe tables render.
	if !strings.Contains(html, "<table>") {
		t.Errorf("/docs contains no rendered table")
	}
	if !strings.Contains(html, "docs-nav") {
		t.Errorf("/docs has no sidebar")
	}

	// Every document is reachable by slug, case-insensitively.
	for _, path := range []string{"/docs/release-spec", "/docs/API", "/docs/RELEASE-SPEC.md"} {
		code, html := getHTML(t, ts, path)
		if code != 200 {
			t.Errorf("GET %s: status %d", path, code)
		}
		if !strings.Contains(html, "docs-body") {
			t.Errorf("GET %s: no document body", path)
		}
	}

	code, html = getHTML(t, ts, "/docs/api")
	if code != 200 || !strings.Contains(html, "REST API") {
		t.Errorf("GET /docs/api: status %d, body has title: %v", code, strings.Contains(html, "REST API"))
	}
	// Both documents are listed in the sidebar.
	for _, title := range []string{"应用发布规范", "REST API"} {
		if !strings.Contains(html, title) {
			t.Errorf("sidebar is missing %q", title)
		}
	}

	// Cross references between documents must survive rendering as site links
	// rather than being dropped by the URL scheme check.
	code, html = getHTML(t, ts, "/docs/upload")
	if code != 200 {
		t.Fatalf("GET /docs/upload: status %d", code)
	}
	if !strings.Contains(html, `href="/docs/release-spec"`) {
		t.Errorf("/docs/upload does not link to the release specification")
	}

	if code, _ := getHTML(t, ts, "/docs/does-not-exist"); code != http.StatusNotFound {
		t.Errorf("unknown document status = %d, want 404", code)
	}
	if code, _ := getHTML(t, ts, "/docs/"); code != http.StatusFound {
		t.Errorf("/docs/ status = %d, want a 302 redirect", code)
	}
}
