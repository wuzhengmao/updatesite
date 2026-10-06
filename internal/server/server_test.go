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
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/wuzhengmao/updatesite/internal/config"
	"github.com/wuzhengmao/updatesite/internal/downloads"
	"github.com/wuzhengmao/updatesite/internal/index"
	"github.com/wuzhengmao/updatesite/internal/server"
	"github.com/wuzhengmao/updatesite/internal/token"
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
	srv, err := server.New(cfg, idx, downloads.New(cfg.DataDir))
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
	for _, want := range []string{
		"发布与维护",
		"/static/upload.js",
		// Shared credentials, used by both forms below.
		`id="f-app"`,
		`id="f-token"`,
		`id="app-ids"`, // suggestion list for the application id
		// Publish form.
		"upload-form",
		`id="drop"`,         // drag and drop target
		`id="progress-bar"`, // upload progress
		`name="file"`,
		`name="version"`,
		`novalidate`, // validation happens in script, a hidden input cannot block it
		// Metadata form.
		"meta-form",
		`id="m-name"`,
		`id="m-description"`,
		`id="m-hidden"`,
		`id="m-icon"`,
		`id="meta-load"`,
		`id="meta-save"`,
		// Version list and destructive actions.
		`id="versions-body"`,
		`id="app-delete"`,
		`id="app-delete-confirm"`,
		`id="confirm-app-input"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("/upload is missing %q", want)
		}
	}

	// Every asset URL must carry the content digest. Without it a browser keeps
	// the stylesheet and script it cached before the upgrade while being served
	// the new HTML, so the page looks like the update did nothing.
	for _, want := range []string{
		"app.css?v=", "app.js?v=", "upload.js?v=", "favicon.svg?v=",
	} {
		if !strings.Contains(html, "/static/"+want) {
			t.Errorf("asset %q is served without a version, so it will be cached stale", want)
		}
	}
	if !reAssetVersion.MatchString(html) {
		t.Error("asset version query is missing or empty")
	}
}

// reAssetVersion matches "/static/<name>?v=<hex>".
var reAssetVersion = regexp.MustCompile(`/static/[A-Za-z0-9._-]+\?v=[0-9a-f]{8,}`)

// Deleting a version removes exactly that version.
func TestDeleteRelease(t *testing.T) {
	ts, dir := newSite(t)
	appID := "demo" // already exists in the fixture, with 1.0.0 and 1.2.0
	tok := token.For(uploadSecret, appID)
	url := ts.URL + "/api/v1/apps/" + appID + "/releases/1.0.0"

	del := func(target, bearer string) (int, map[string]any) {
		t.Helper()
		req, err := http.NewRequest(http.MethodDelete, target, nil)
		if err != nil {
			t.Fatal(err)
		}
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

	if code, _ := del(url, ""); code != http.StatusUnauthorized {
		t.Errorf("without a token: status %d, want 401", code)
	}
	if code, _ := del(url, token.For(uploadSecret, "otherapp")); code != http.StatusUnauthorized {
		t.Errorf("with another app's token: status %d, want 401", code)
	}

	code, body := del(url, tok)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %v", code, body)
	}
	if _, err := os.Stat(filepath.Join(dir, "apps", appID, "1.0.0")); !os.IsNotExist(err) {
		t.Error("the version directory is still there")
	}
	// The sibling version and the app metadata survive.
	for _, p := range []string{
		filepath.Join(dir, "apps", appID, "1.2.0"),
		filepath.Join(dir, "apps", appID, "app.json"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s should have been left alone: %v", p, err)
		}
	}

	if code, _ := del(url, tok); code != http.StatusNotFound {
		t.Errorf("deleting again: status %d, want 404", code)
	}
	// "latest" is not a directory name, so it must not silently resolve.
	if code, _ := del(ts.URL+"/api/v1/apps/"+appID+"/releases/latest", tok); code == http.StatusOK {
		t.Error(`DELETE .../releases/latest was accepted`)
	}
}

func TestDeleteApp(t *testing.T) {
	ts, dir := newSite(t)
	appID := "edge" // exists in the fixture with a pre-release
	tok := token.For(uploadSecret, appID)
	url := ts.URL + "/api/v1/apps/" + appID

	req, _ := http.NewRequest(http.MethodDelete, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("without a token: status %d, want 401", resp.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodDelete, url, nil)
	req.Header.Set("X-Upload-Token", tok)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.StatusCode, body)
	}
	if _, err := os.Stat(filepath.Join(dir, "apps", appID)); !os.IsNotExist(err) {
		t.Error("the application directory is still there")
	}
	// The other application is untouched.
	if _, err := os.Stat(filepath.Join(dir, "apps", "demo")); err != nil {
		t.Errorf("a sibling application was removed: %v", err)
	}
}

// The metadata endpoints let an administrator set an application's identity
// without hand editing app.json.
func TestMetadataEndpoints(t *testing.T) {
	ts, dir := newSite(t)
	appID := "metaapp"
	tok := token.For(uploadSecret, appID)
	base := ts.URL + "/api/v1/apps/" + appID + "/metadata"

	call := func(method, body string) (int, map[string]any) {
		t.Helper()
		var reader io.Reader
		if body != "" {
			reader = strings.NewReader(body)
		}
		req, err := http.NewRequest(method, base, reader)
		if err != nil {
			t.Fatal(err)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	// Authentication applies here too.
	req, _ := http.NewRequest(http.MethodGet, base, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET without a token: status %d, want 401", resp.StatusCode)
	}

	// An application with no app.json reads back as an empty object.
	code, body := call(http.MethodGet, "")
	if code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200: %v", code, body)
	}
	if meta, ok := body["metadata"].(map[string]any); !ok || meta["name"] != "" {
		t.Errorf("metadata = %v, want an empty object", body["metadata"])
	}

	code, body = call(http.MethodPut,
		`{"name":"元数据应用","vendor":"MTI","tags":["a","b"],"hidden":true,"order":7}`)
	if code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200: %v", code, body)
	}
	stored, err := os.ReadFile(filepath.Join(dir, "apps", appID, "app.json"))
	if err != nil {
		t.Fatalf("app.json was not written: %v", err)
	}
	for _, want := range []string{"元数据应用", "MTI", `"hidden": true`, `"order": 7`} {
		if !strings.Contains(string(stored), want) {
			t.Errorf("app.json is missing %q:\n%s", want, stored)
		}
	}

	for _, c := range []struct{ name, body, wantCode string }{
		{"broken json", `{"name":`, "invalid_json"},
		{"unknown field", `{"nope":"x"}`, "invalid_json"},
		{"bad homepage", `{"homepage":"example.com"}`, "save_failed"},
	} {
		code, body := call(http.MethodPut, c.body)
		if code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400 (%v)", c.name, code, body)
			continue
		}
		if errObj, _ := body["error"].(map[string]any); errObj["code"] != c.wantCode {
			t.Errorf("%s: error code = %v, want %q", c.name, errObj["code"], c.wantCode)
		}
	}
}

func TestIconUpload(t *testing.T) {
	ts, dir := newSite(t)
	appID := "iconapp"
	tok := token.For(uploadSecret, appID)
	base := ts.URL + "/api/v1/apps/" + appID + "/icon"

	put := func(data []byte) (int, map[string]any) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPut, base, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("x"), 64)...)
	code, body := put(png)
	if code != http.StatusOK {
		t.Fatalf("PUT icon status = %d, want 200: %v", code, body)
	}
	// The file name comes from the sniffed content, never from the request.
	if _, err := os.Stat(filepath.Join(dir, "apps", appID, "icon.png")); err != nil {
		t.Errorf("icon.png was not written: %v", err)
	}
	// app.json must point at it, or a previously configured custom name would
	// keep winning when the scanner resolves the icon.
	stored, _ := os.ReadFile(filepath.Join(dir, "apps", appID, "app.json"))
	if !strings.Contains(string(stored), `"icon": "icon.png"`) {
		t.Errorf("app.json does not reference the new icon:\n%s", stored)
	}

	// An SVG replaces the PNG, and the PNG is cleaned up.
	svg := []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"><rect/></svg>`)
	if code, body := put(svg); code != http.StatusOK {
		t.Fatalf("PUT svg status = %d: %v", code, body)
	}
	if _, err := os.Stat(filepath.Join(dir, "apps", appID, "icon.svg")); err != nil {
		t.Errorf("icon.svg was not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "apps", appID, "icon.png")); !os.IsNotExist(err) {
		t.Error("the previous icon.png was left behind")
	}

	for _, c := range []struct {
		name string
		data []byte
	}{
		{"not an image", []byte("just some text")},
		{"html posing as svg", []byte(`<html><script>alert(1)</script></html>`)},
		{"empty", nil},
	} {
		if code, body := put(c.data); code == http.StatusOK {
			t.Errorf("%s: icon was accepted (%v)", c.name, body)
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
	ts, dir, _ := newSiteWithStore(t)
	return ts, dir
}

// newSiteWithStore is newSite plus the download counter, for the tests that
// inspect or persist the counts.
func newSiteWithStore(t *testing.T) (*httptest.Server, string, *downloads.Store) {
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

	// A third app whose only artifact is hosted elsewhere: the site redirects
	// to it and cannot tell whether the file was ever fetched.
	write(t, dir, "apps/remote/1.0.0/release.json", `{
		"artifacts": [
			{"file": "Remote-1.0.0-windows-x64.exe", "url": "https://example.com/Remote-1.0.0-windows-x64.exe"}
		]
	}`)

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

	// The counter is not run on a flush loop here: a background Save racing the
	// t.TempDir cleanup would leave files behind. Tests that need persistence
	// call Save themselves; Run has its own tests in internal/downloads.
	dl := downloads.New(cfg.DataDir)

	srv, err := server.New(cfg, idx, dl)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, dir, dl
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
	if len(apps) != 3 {
		t.Fatalf("got %d apps, want 3: %v", len(apps), body)
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

// fetch performs a GET and returns the status with the body drained, so the
// server sees a complete download.
func fetch(t *testing.T, ts *httptest.Server, path string, header http.Header) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp
}

// apiDownloads reads the downloads field of an app object.
func apiDownloads(t *testing.T, ts *httptest.Server, app string) int64 {
	t.Helper()
	code, body := getJSON(t, ts, "/api/v1/apps/"+app)
	if code != http.StatusOK {
		t.Fatalf("GET /api/v1/apps/%s: status %d", app, code)
	}
	n, ok := body["downloads"].(float64)
	if !ok {
		t.Fatalf("app object has no downloads field: %v", body)
	}
	return int64(n)
}

func TestDownloadCounts(t *testing.T) {
	ts, _, dl := newSiteWithStore(t)
	const exe = "Demo-1.2.0-windows-x64.exe"

	// The same file twice, plus the two other spellings of the same version:
	// "latest" and a "v" prefix must fold into the concrete release.
	fetch(t, ts, "/dl/demo/1.2.0/"+exe, nil)
	fetch(t, ts, "/dl/demo/latest/"+exe, nil)
	fetch(t, ts, "/dl/demo/v1.2.0/"+exe, nil)
	fetch(t, ts, "/dl/demo/1.2.0/Demo-1.2.0-macos-universal.dmg", nil)

	if got := dl.FileCount("demo", "1.2.0", exe); got != 3 {
		t.Errorf("file count = %d, want 3", got)
	}
	if got := dl.VersionCount("demo", "1.2.0"); got != 4 {
		t.Errorf("version count = %d, want 4", got)
	}
	if got := dl.AppCount("demo"); got != 4 {
		t.Errorf("app count = %d, want 4", got)
	}
	if got := dl.VersionCount("demo", "1.0.0"); got != 0 {
		t.Errorf("the untouched version = %d, want 0", got)
	}

	// The numbers reach the API, at every level.
	code, body := getJSON(t, ts, "/api/v1/apps/demo?releases=1")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got, _ := body["downloads"].(float64); int64(got) != 4 {
		t.Errorf("app downloads = %v, want 4", body["downloads"])
	}
	releases, _ := body["releases"].([]any)
	if len(releases) == 0 {
		t.Fatal("the app object carries no releases")
	}
	first, _ := releases[0].(map[string]any)
	if got, _ := first["downloads"].(float64); int64(got) != 4 {
		t.Errorf("release downloads = %v, want 4", first["downloads"])
	}
	found := false
	for _, a := range first["artifacts"].([]any) {
		art, _ := a.(map[string]any)
		if art["file"] == exe {
			found = true
			if got, _ := art["downloads"].(float64); int64(got) != 3 {
				t.Errorf("artifact downloads = %v, want 3", art["downloads"])
			}
		}
	}
	if !found {
		t.Errorf("artifact %s is missing from the release", exe)
	}

	// A rescan replaces the catalogue; the counter is keyed by name and stays.
	if resp, err := http.Post(ts.URL+"/api/v1/rescan", "", nil); err != nil {
		t.Fatal(err)
	} else {
		resp.Body.Close()
	}
	if got := apiDownloads(t, ts, "demo"); got != 4 {
		t.Errorf("downloads after a rescan = %d, want 4", got)
	}
}

func TestDownloadCountsIgnoreHeadAndNotModified(t *testing.T) {
	ts, _, dl := newSiteWithStore(t)
	const path = "/dl/demo/1.2.0/Demo-1.2.0-windows-x64.exe"

	resp := fetch(t, ts, path, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Fatal("the artifact has no ETag to revalidate against")
	}

	// HEAD is routed to the same handler but transfers nothing.
	head, err := http.NewRequest(http.MethodHead, ts.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	headResp, err := http.DefaultClient.Do(head)
	if err != nil {
		t.Fatal(err)
	}
	headResp.Body.Close()

	// A conditional request answered with 304 is a revalidation, not a download.
	if got := fetch(t, ts, path, http.Header{"If-None-Match": {etag}}).StatusCode; got != http.StatusNotModified {
		t.Fatalf("revalidation status = %d, want 304", got)
	}
	if got := fetch(t, ts, path, http.Header{"If-Modified-Since": {"Mon, 02 Jan 2030 15:04:05 GMT"}}).StatusCode; got != http.StatusNotModified {
		t.Fatalf("If-Modified-Since status = %d, want 304", got)
	}

	if got := dl.FileCount("demo", "1.2.0", "Demo-1.2.0-windows-x64.exe"); got != 1 {
		t.Errorf("file count = %d, want 1: HEAD and revalidations are not downloads", got)
	}
}

func TestDownloadCountsIgnoreExternal(t *testing.T) {
	ts, _, dl := newSiteWithStore(t)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Get(ts.URL + "/dl/remote/1.0.0/Remote-1.0.0-windows-x64.exe")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
	if got := dl.AppCount("remote"); got != 0 {
		t.Errorf("external redirects counted %d downloads, want 0", got)
	}
}

func TestDownloadCountsPersist(t *testing.T) {
	ts, dir, dl := newSiteWithStore(t)
	fetch(t, ts, "/dl/demo/1.2.0/Demo-1.2.0-windows-x64.exe", nil)
	dl.Save()

	reloaded := downloads.New(dir)
	if got := reloaded.FileCount("demo", "1.2.0", "Demo-1.2.0-windows-x64.exe"); got != 1 {
		t.Errorf("file count after a reload = %d, want 1", got)
	}
	if got := reloaded.Total(); got != 1 {
		t.Errorf("total after a reload = %d, want 1", got)
	}
}

func TestDownloadCountsRendered(t *testing.T) {
	ts, _, _ := newSiteWithStore(t)
	for i := 0; i < 2; i++ {
		fetch(t, ts, "/dl/demo/1.2.0/Demo-1.2.0-windows-x64.exe", nil)
	}

	code, html := getHTML(t, ts, "/")
	if code != http.StatusOK {
		t.Fatalf("index status = %d", code)
	}
	if !strings.Contains(html, "2 个版本 · 累计下载 2 次 · 最近更新") {
		t.Error("the index card does not show the download count")
	}

	code, html = getHTML(t, ts, "/a/demo")
	if code != http.StatusOK {
		t.Fatalf("app page status = %d", code)
	}
	for _, want := range []string{
		`<th class="num">下载</th>`,
		"累计下载 2 次",
		"个文件 · 累计下载 2 次",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the app page does not contain %q", want)
		}
	}
}

func TestHealthReportsDownloads(t *testing.T) {
	ts, _, _ := newSiteWithStore(t)
	fetch(t, ts, "/dl/demo/1.2.0/Demo-1.2.0-windows-x64.exe", nil)

	code, body := getJSON(t, ts, "/api/v1/health")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	stats, ok := body["downloads"].(map[string]any)
	if !ok {
		t.Fatalf("health has no downloads object: %v", body)
	}
	if got, _ := stats["total"].(float64); int64(got) != 1 {
		t.Errorf("total = %v, want 1", stats["total"])
	}
	if stats["writable"] != true {
		t.Errorf("writable = %v, want true", stats["writable"])
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
