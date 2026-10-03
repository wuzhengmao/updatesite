package upload

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ------------------------------------------------------------ test archives

// zipOf builds a zip archive in memory. Entries ending in "/" become
// directories; an entry name of the form "link:target" becomes a symlink.
func zipOf(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range entries {
		if strings.HasPrefix(body, "link:") {
			hdr := &zip.FileHeader{Name: name, Method: zip.Store}
			hdr.SetMode(os.ModeSymlink | 0o777)
			w, err := zw.CreateHeader(hdr)
			if err != nil {
				t.Fatal(err)
			}
			w.Write([]byte(strings.TrimPrefix(body, "link:")))
			continue
		}
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

func tarGzOf(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for name, body := range entries {
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}
		if strings.HasPrefix(body, "link:") {
			hdr.Typeflag = tar.TypeSymlink
			hdr.Linkname = strings.TrimPrefix(body, "link:")
			hdr.Size = 0
		} else {
			hdr.Typeflag = tar.TypeReg
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if hdr.Size > 0 {
			tw.Write([]byte(body))
		}
	}
	tw.Close()
	gw.Close()
	return buf.Bytes()
}

// publish extracts data as if it had been uploaded.
func publish(t *testing.T, data []byte, format Format, opts Options) (*Result, error) {
	t.Helper()
	r := bytes.NewReader(data)
	return Publish(r, int64(len(data)), format, opts)
}

func newOpts(t *testing.T, appID, version string) Options {
	t.Helper()
	return Options{AppsDir: t.TempDir(), AppID: appID, Version: version}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

// ---------------------------------------------------------------- happy path

func TestPublishZipWithExplicitVersion(t *testing.T) {
	opts := newOpts(t, "myapp", "1.2.0")
	res, err := publish(t, zipOf(t, map[string]string{
		"MyApp-1.2.0-windows-x64.exe":    "win",
		"MyApp-1.2.0-linux-arm64.tar.gz": "linux",
		"CHANGELOG.md":                   "## 1.2.0",
		"release.json":                   `{"channel":"stable"}`,
	}), FormatZip, opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != "1.2.0" || res.App != "myapp" || res.Replaced {
		t.Fatalf("unexpected result: %+v", res)
	}
	if len(res.Files) != 4 {
		t.Errorf("got %d files, want 4: %+v", len(res.Files), res.Files)
	}
	got := readFile(t, filepath.Join(opts.AppsDir, "myapp", "1.2.0", "MyApp-1.2.0-windows-x64.exe"))
	if got != "win" {
		t.Errorf("file content = %q", got)
	}
}

func TestPublishTarGz(t *testing.T) {
	opts := newOpts(t, "myapp", "2.0.0")
	if _, err := publish(t, tarGzOf(t, map[string]string{
		"MyApp-2.0.0-windows-x64.exe": "win",
	}), FormatTarGz, opts); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(opts.AppsDir, "myapp", "2.0.0", "MyApp-2.0.0-windows-x64.exe")); got != "win" {
		t.Errorf("content = %q", got)
	}
}

// ------------------------------------------------------------------ version

func TestVersionFromSingleTopDirectory(t *testing.T) {
	opts := newOpts(t, "myapp", "")
	res, err := publish(t, zipOf(t, map[string]string{
		"3.1.0/App-3.1.0-windows-x64.exe": "win",
		"3.1.0/CHANGELOG.md":              "notes",
	}), FormatZip, opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != "3.1.0" {
		t.Fatalf("version = %q, want 3.1.0", res.Version)
	}
	// The wrapper directory must be stripped from the reported paths.
	for _, f := range res.Files {
		if strings.HasPrefix(f.Path, "3.1.0/") {
			t.Errorf("file path still carries the wrapper: %q", f.Path)
		}
	}
	if got := readFile(t, filepath.Join(opts.AppsDir, "myapp", "3.1.0", "App-3.1.0-windows-x64.exe")); got != "win" {
		t.Errorf("content = %q", got)
	}
}

func TestVersionFromGitHubStyleDirectory(t *testing.T) {
	for dir, want := range map[string]string{
		"myapp-1.2.0":     "1.2.0",
		"App-1.2.3-rc.1":  "1.2.3-rc.1",
		"app-v2-1.0.0":    "1.0.0",
		"some_repo_2.5.1": "2.5.1",
	} {
		opts := newOpts(t, "myapp", "")
		res, err := publish(t, zipOf(t, map[string]string{
			dir + "/App-" + want + "-windows-x64.exe": "win",
		}), FormatZip, opts)
		if err != nil {
			t.Errorf("%s: %v", dir, err)
			continue
		}
		if res.Version != want {
			t.Errorf("%s: version = %q, want %q", dir, res.Version, want)
		}
	}
}

func TestVersionFromManifest(t *testing.T) {
	opts := newOpts(t, "myapp", "")
	res, err := publish(t, zipOf(t, map[string]string{
		"release.json":                 `{"version":"4.5.6","channel":"beta"}`,
		"App-4.5.6-windows-x64.exe":    "win",
		"App-4.5.6-macos-arm64.pkg":    "mac",
		"loose-file-not-a-directory":   "x",
		"another-loose-file.txt":       "y",
		"App-4.5.6-linux-x64.tar.gz":   "linux",
		"App-4.5.6-linux-arm64.tar.gz": "linux2",
	}), FormatZip, opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != "4.5.6" {
		t.Fatalf("version = %q, want 4.5.6", res.Version)
	}
}

func TestVersionCannotBeDetermined(t *testing.T) {
	opts := newOpts(t, "myapp", "")
	_, err := publish(t, zipOf(t, map[string]string{
		"App-windows-x64.exe": "win",
		"readme.txt":          "hi",
	}), FormatZip, opts)
	if err == nil {
		t.Fatal("expected an error when the version cannot be worked out")
	}
	if !strings.Contains(err.Error(), "version") {
		t.Errorf("error should explain the version problem, got: %v", err)
	}
}

func TestRejectsInvalidVersion(t *testing.T) {
	opts := newOpts(t, "myapp", "../escape")
	if _, err := publish(t, zipOf(t, map[string]string{"a.exe": "x"}), FormatZip, opts); err == nil {
		t.Fatal("expected an invalid version to be refused")
	}
}

// ----------------------------------------------------------------- overwrite

func TestOverwriteExistingVersion(t *testing.T) {
	opts := newOpts(t, "myapp", "1.0.0")

	if _, err := publish(t, zipOf(t, map[string]string{"old.exe": "old"}), FormatZip, opts); err != nil {
		t.Fatal(err)
	}
	res, err := publish(t, zipOf(t, map[string]string{"new.exe": "new"}), FormatZip, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Replaced {
		t.Error("second publish should report Replaced")
	}

	dir := filepath.Join(opts.AppsDir, "myapp", "1.0.0")
	if _, err := os.Stat(filepath.Join(dir, "old.exe")); !os.IsNotExist(err) {
		t.Error("the previous release's file survived the replacement")
	}
	if got := readFile(t, filepath.Join(dir, "new.exe")); got != "new" {
		t.Errorf("content = %q", got)
	}
	// No staging or trash directories may be left behind.
	assertCleanAppDir(t, filepath.Join(opts.AppsDir, "myapp"))
}

func assertCleanAppDir(t *testing.T, appDir string) {
	t.Helper()
	entries, err := os.ReadDir(appDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Errorf("leftover temporary entry %q in %s", e.Name(), appDir)
		}
	}
}

// ------------------------------------------------------------------ security

func TestRejectsPathTraversal(t *testing.T) {
	for _, name := range []string{"../evil.exe", "../../evil.exe", "a/../../evil.exe"} {
		opts := newOpts(t, "myapp", "1.0.0")
		_, err := publish(t, zipOf(t, map[string]string{
			name:                        "pwned",
			"App-1.0.0-windows-x64.exe": "ok",
		}), FormatZip, opts)
		if err == nil {
			t.Errorf("entry %q was accepted", name)
		}
		if _, statErr := os.Stat(filepath.Join(opts.AppsDir, "evil.exe")); !os.IsNotExist(statErr) {
			t.Errorf("entry %q escaped the release directory", name)
		}
	}
}

func TestSkipsAbsoluteAndSymlinkEntries(t *testing.T) {
	opts := newOpts(t, "myapp", "1.0.0")
	res, err := publish(t, tarGzOf(t, map[string]string{
		"App-1.0.0-windows-x64.exe": "win",
		"escape":                    "link:/etc/passwd",
		"/etc/absolute":             "nope",
	}), FormatTarGz, opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res.Files {
		if f.Path == "escape" {
			t.Error("a symlink entry was extracted")
		}
	}
	if len(res.Files) != 1 {
		t.Errorf("expected only the real file, got %+v", res.Files)
	}
}

func TestSkipsMacOSNoise(t *testing.T) {
	opts := newOpts(t, "myapp", "1.0.0")
	res, err := publish(t, zipOf(t, map[string]string{
		"App-1.0.0-windows-x64.exe":            "win",
		"__MACOSX/._App-1.0.0-windows-x64.exe": "junk",
		".DS_Store":                            "junk",
		".git/config":                          "junk",
	}), FormatZip, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 1 || res.Files[0].Path != "App-1.0.0-windows-x64.exe" {
		t.Errorf("unexpected files: %+v", res.Files)
	}
	entries, _ := os.ReadDir(filepath.Join(opts.AppsDir, "myapp", "1.0.0"))
	if len(entries) != 1 {
		t.Errorf("junk was written to disk: %v", entries)
	}
}

func TestRejectsMetadataOnlyArchive(t *testing.T) {
	opts := newOpts(t, "myapp", "1.0.0")
	_, err := publish(t, zipOf(t, map[string]string{
		"release.json": `{"version":"1.0.0"}`,
		"CHANGELOG.md": "notes",
	}), FormatZip, opts)
	if err == nil || !strings.Contains(err.Error(), "no installable files") {
		t.Fatalf("expected a metadata-only rejection, got: %v", err)
	}
}

func TestEnforcesExpansionLimit(t *testing.T) {
	opts := newOpts(t, "myapp", "1.0.0")
	opts.Limits = Limits{MaxTotalBytes: 10, MaxEntries: 100}
	_, err := publish(t, zipOf(t, map[string]string{
		"App-1.0.0-windows-x64.exe": strings.Repeat("x", 1000),
	}), FormatZip, opts)
	if err == nil {
		t.Fatal("expected the expansion limit to be enforced")
	}
}

func TestEnforcesEntryLimit(t *testing.T) {
	entries := map[string]string{"App-1.0.0-windows-x64.exe": "x"}
	for i := 0; i < 20; i++ {
		entries["file"+string(rune('a'+i))+".bin"] = "x"
	}
	opts := newOpts(t, "myapp", "1.0.0")
	opts.Limits = Limits{MaxTotalBytes: 1 << 20, MaxEntries: 5}
	if _, err := publish(t, zipOf(t, entries), FormatZip, opts); err == nil {
		t.Fatal("expected the entry limit to be enforced")
	}
}

// ------------------------------------------------------------------ guessing

func TestValidateAppID(t *testing.T) {
	for _, ok := range []string{"myapp", "my-app", "my_app", "app.v2", "App2"} {
		if err := ValidateAppID(ok); err != nil {
			t.Errorf("ValidateAppID(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", ".", "..", ".hidden", "a/b", `a\b`, "a\x00b"} {
		if err := ValidateAppID(bad); err == nil {
			t.Errorf("ValidateAppID(%q) = nil, want an error", bad)
		}
	}
}

func TestSniff(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want Format
	}{
		{"zip", zipOf(t, map[string]string{"a.txt": "x"}), FormatZip},
		{"tar.gz", tarGzOf(t, map[string]string{"a.txt": "x"}), FormatTarGz},
	}
	for _, c := range cases {
		got, ok := Sniff(c.data)
		if !ok || got != c.want {
			t.Errorf("Sniff(%s) = %q, %v; want %q", c.name, got, ok, c.want)
		}
	}
	if _, ok := Sniff([]byte("not an archive at all, just text")); ok {
		t.Error("Sniff accepted plain text")
	}
}

func TestUnsupportedHint(t *testing.T) {
	if hint := UnsupportedHint("release.tar.xz"); !strings.Contains(hint, "tar.xz") {
		t.Errorf("hint = %q", hint)
	}
	if hint := UnsupportedHint("release.zip"); hint != "" {
		t.Errorf("zip should not produce a hint, got %q", hint)
	}
}
