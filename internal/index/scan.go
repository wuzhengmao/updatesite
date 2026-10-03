package index

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	appMetaFile     = "app.json"
	releaseMetaFile = "release.json"
	publishedFile   = ".published"
	defaultOrder    = 100
)

// notesCandidates are probed in order; the first existing file becomes the
// release changelog.
var notesCandidates = []string{
	"CHANGELOG.md", "changelog.md", "Changelog.md",
	"CHANGES.md", "changes.md",
	"RELEASE_NOTES.md", "release-notes.md", "RELEASENOTES.md",
	"NOTES.md", "notes.md",
}

// docPrefixes mark files that are documentation rather than downloads. They are
// never listed as artifacts.
var docPrefixes = []string{
	"changelog", "changes", "releasenotes", "release-notes",
	"notes", "readme", "license", "licence", "copying",
}

// checksumFileNames are checksum manifests consumed by the scanner instead of
// being listed as artifacts.
var checksumFileNames = map[string]bool{
	"sha256sums": true, "sha256sums.txt": true, "sha256sum.txt": true,
	"checksums.txt": true, "checksums.sha256": true, "sums.txt": true,
}

// appMeta is the optional app.json file.
type appMeta struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Summary     string   `json:"summary"`
	Description string   `json:"description"`
	Homepage    string   `json:"homepage"`
	Vendor      string   `json:"vendor"`
	License     string   `json:"license"`
	Tags        []string `json:"tags"`
	Platforms   []string `json:"platforms"`
	Channel     string   `json:"channel"`
	Icon        string   `json:"icon"`
	Order       *int     `json:"order"`
	Hidden      bool     `json:"hidden"`
}

// releaseMeta is the optional release.json file.
type releaseMeta struct {
	Version     string         `json:"version"`
	Channel     string         `json:"channel"`
	Title       string         `json:"title"`
	Notes       string         `json:"notes"`
	PublishedAt string         `json:"publishedAt"`
	Prerelease  *bool          `json:"prerelease"`
	Mandatory   bool           `json:"mandatory"`
	MinVersion  string         `json:"minVersion"`
	Artifacts   []artifactMeta `json:"artifacts"`
}

type artifactMeta struct {
	File   string `json:"file"`
	Name   string `json:"name"`
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	Kind   string `json:"kind"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	URL    string `json:"url"`
}

// Scanner walks the archive directory and builds a Snapshot.
type Scanner struct {
	appsDir string
	sums    *sumCache
}

func newScanner(appsDir string, sums *sumCache) *Scanner {
	return &Scanner{appsDir: appsDir, sums: sums}
}

// Scan walks the archive. Files whose digest is not known yet are returned as
// absolute paths so the caller can hash them and scan again.
func (s *Scanner) Scan() (*Snapshot, []string) {
	snap := &Snapshot{
		BuiltAt: time.Now(),
		AppsDir: s.appsDir,
		byID:    map[string]*App{},
	}
	var pending []string

	entries, err := os.ReadDir(s.appsDir)
	if err != nil {
		if !os.IsNotExist(err) {
			snap.warn("cannot read %s: %v", s.appsDir, err)
		}
		return snap, nil
	}

	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		app := s.scanApp(filepath.Join(s.appsDir, e.Name()), e.Name(), snap, &pending)
		if app == nil {
			continue
		}
		snap.Apps = append(snap.Apps, app)
	}
	// Keep the id map aligned with the emitted slice.
	for _, a := range snap.Apps {
		snap.byID[a.ID] = a
	}

	sort.SliceStable(snap.Apps, func(i, j int) bool {
		if snap.Apps[i].Order != snap.Apps[j].Order {
			return snap.Apps[i].Order < snap.Apps[j].Order
		}
		return strings.ToLower(snap.Apps[i].Name) < strings.ToLower(snap.Apps[j].Name)
	})

	return snap, pending
}

// Apps returns the applications that should be listed publicly.
func (s *Snapshot) PublicApps() []*App {
	out := make([]*App, 0, len(s.Apps))
	for _, a := range s.Apps {
		if !a.Hidden {
			out = append(out, a)
		}
	}
	return out
}

func (s *Snapshot) warn(format string, args ...any) {
	s.Warnings = append(s.Warnings, fmt.Sprintf(format, args...))
}

func (s *Scanner) scanApp(dir, dirName string, snap *Snapshot, pending *[]string) *App {
	app := &App{
		ID:      dirName,
		Name:    dirName,
		Channel: DefaultChannel,
		Dir:     dir,
		Order:   defaultOrder,
	}

	meta, ok := readJSON[appMeta](filepath.Join(dir, appMetaFile), snap)
	if ok {
		if meta.ID != "" && meta.ID != dirName {
			snap.warn("%s: app.json id %q ignored, the directory name %q wins", dirName, meta.ID, dirName)
		}
		if meta.Name != "" {
			app.Name = meta.Name
		}
		app.Summary = meta.Summary
		app.Description = meta.Description
		app.Homepage = meta.Homepage
		app.Vendor = meta.Vendor
		app.License = meta.License
		app.Tags = meta.Tags
		app.Platforms = meta.Platforms
		app.Hidden = meta.Hidden
		if meta.Channel != "" {
			app.Channel = meta.Channel
		}
		if meta.Order != nil {
			app.Order = *meta.Order
		}
	}

	app.iconSrc = resolveIcon(dir, meta.Icon)
	if app.iconSrc != "" {
		app.IconURL = "/a/" + app.ID + "/icon"
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		snap.warn("%s: %v", dirName, err)
		return app
	}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || strings.HasPrefix(name, ".") {
			continue
		}
		versionDir := filepath.Join(dir, name)
		if !IsVersionDir(name) {
			if _, err := os.Stat(filepath.Join(versionDir, releaseMetaFile)); err == nil {
				// A release.json makes any directory name authoritative.
			} else {
				snap.warn("%s: skipping %q, not a version directory", dirName, name)
				continue
			}
		}
		rel, _ := s.scanRelease(app, versionDir, name, snap, pending)
		if rel != nil {
			app.Releases = append(app.Releases, rel)
		}
	}
	app.finalize()
	return app
}

func (s *Scanner) scanRelease(app *App, dir, version string, snap *Snapshot, pending *[]string) (*Release, error) {
	rel := &Release{
		App:     app.ID,
		Version: version,
		Channel: app.Channel,
		Dir:     dir,
	}

	meta, _ := readJSON[releaseMeta](filepath.Join(dir, releaseMetaFile), snap)
	if meta.Version != "" && meta.Version != version {
		snap.warn("%s/%s: release.json version %q ignored, the directory name wins", app.ID, version, meta.Version)
	}
	if meta.Channel != "" {
		rel.Channel = meta.Channel
	}
	rel.Title = meta.Title
	rel.Notes = meta.Notes
	rel.Mandatory = meta.Mandatory
	rel.MinVersion = meta.MinVersion

	rel.PublishedAt = publishTime(dir, meta.PublishedAt)
	rel.Prerelease = isPrerelease(rel.Version, meta.Prerelease)
	if notes := readNotes(dir); notes != "" {
		rel.Notes = notes
	}

	// Checksums declared out of band: sidecars and SHA256SUMS manifests.
	sidecars, manifests := collectChecksums(dir, snap)

	// Auto-detected artifacts.
	declared := map[string]bool{}
	for _, am := range meta.Artifacts {
		declared[normalizeRel(am.File)] = true
	}
	files, err := listFiles(dir, snap, app.ID+"/"+version)
	if err != nil {
		return rel, err
	}
	for _, f := range files {
		if IsMetadataFile(f) {
			continue
		}
		if declared[f] {
			continue // handled below, keeping the declared field order
		}
		rel.Artifacts = append(rel.Artifacts, s.buildArtifact(app, rel, f, artifactMeta{}, sidecars, manifests, pending))
	}

	// Declared artifacts, in the order the manifest lists them.
	for _, am := range meta.Artifacts {
		file := normalizeRel(am.File)
		if !validRel(file) {
			snap.warn("%s/%s: ignoring artifact path %q", app.ID, version, am.File)
			continue
		}
		rel.Artifacts = append(rel.Artifacts, s.buildArtifact(app, rel, file, am, sidecars, manifests, pending))
	}

	if len(rel.Artifacts) == 0 {
		snap.warn("%s/%s: no artifacts found", app.ID, version)
	}
	return rel, nil
}

// buildArtifact merges the file on disk with any declared metadata.
func (s *Scanner) buildArtifact(app *App, rel *Release, file string, decl artifactMeta, sidecars, manifests map[string]string, pending *[]string) *Artifact {
	abs := filepath.Join(rel.Dir, filepath.FromSlash(file))
	a := &Artifact{
		File:   file,
		Name:   filepath.Base(file),
		Path:   DownloadPath(app.ID, rel.Version, file),
		Kind:   "other",
		OS:     NormalizeOS(decl.OS),
		Arch:   NormalizeArch(decl.Arch),
		Size:   decl.Size,
		SHA256: strings.ToLower(decl.SHA256),
	}

	if decl.Kind != "" {
		a.Kind = NormalizeKind(decl.Kind)
	}
	if !hasPrefixFold(decl.URL, "http://") && !hasPrefixFold(decl.URL, "https://") {
		if decl.URL != "" {
			// Only absolute http(s) targets are honoured.
			decl.URL = ""
		}
	}
	a.External = decl.URL

	if decl.Name != "" {
		a.Name = decl.Name
	}

	st, err := os.Stat(abs)
	local := err == nil && st.Mode().IsRegular()
	switch {
	case local:
		a.Available = true
		a.Modified = st.ModTime()
		if a.Size <= 0 {
			a.Size = st.Size()
		}
		if a.SHA256 == "" {
			a.SHA256 = sidecars[file]
		}
		if a.SHA256 == "" {
			a.SHA256 = manifests[strings.ToLower(filepath.Base(file))]
		}
		if a.SHA256 == "" {
			size, mtime := st.Size(), st.ModTime().UnixNano()
			if sum, ok := s.sums.Lookup(abs, size, mtime); ok {
				a.SHA256 = sum
			} else if !s.sums.Failed(abs, size, mtime) {
				*pending = append(*pending, abs)
			}
		}
	case a.External != "":
		a.Available = true // hosted elsewhere but still installable
	default:
		a.Available = false
	}

	if a.OS == "" || a.Arch == "" || decl.Kind == "" {
		os, arch, kind := Detect(filepath.Base(file))
		if a.OS == "" {
			a.OS = os
		}
		if a.Arch == "" {
			a.Arch = arch
		}
		if decl.Kind == "" {
			a.Kind = kind
		}
		if decl.OS == "" && decl.Arch == "" {
			a.Detected = true
		}
	}
	if !local {
		a.Path = "" // nothing to serve locally, clients use externalUrl
	}
	return a
}

// listFiles returns every regular file below dir as a "/"-separated relative
// path, skipping dot files and dot directories.
func listFiles(dir string, snap *Snapshot, label string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			snap.warn("%s: %v", label, err)
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if p != dir && strings.HasPrefix(name, ".") {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") {
			return nil
		}
		// os.Stat follows symlinks so that a release may link to a large
		// artifact stored elsewhere on the volume.
		if st, serr := os.Stat(p); serr != nil || !st.Mode().IsRegular() {
			return nil
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(out)
	return out, err
}

// IsMetadataFile reports whether a relative file name is release metadata
// rather than a downloadable artifact. It is exported so the upload path can
// apply exactly the same rule when validating an archive.
func IsMetadataFile(rel string) bool {
	base := filepath.Base(rel)
	lower := strings.ToLower(base)
	if lower == appMetaFile || lower == releaseMetaFile || lower == publishedFile {
		return true
	}
	if checksumFileNames[lower] {
		return true
	}
	if isChecksumSidecar(lower) {
		return true
	}
	stem := strings.TrimSuffix(lower, Extension(lower))
	for _, p := range docPrefixes {
		if strings.HasPrefix(stem, p) {
			return true
		}
	}
	return false
}

func isChecksumSidecar(lower string) bool {
	for _, ext := range []string{".sha256", ".sha256sum", ".sha1", ".md5", ".sums"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// collectChecksums reads "<file>.sha256" sidecars and SHA256SUMS manifests from
// the top level of a version directory. Manifests are keyed by lower-case base
// name so they can be matched from nested paths too.
func collectChecksums(dir string, snap *Snapshot) (sidecars, manifests map[string]string) {
	sidecars = map[string]string{}
	manifests = map[string]string{}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return sidecars, manifests
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		lower := strings.ToLower(name)

		if isChecksumSidecar(lower) {
			// The base name keeps its original case and separators.
			base := name
			for _, ext := range []string{".sha256sum", ".sha256", ".sha1", ".md5", ".sums"} {
				if strings.HasSuffix(lower, ext) {
					base = name[:len(name)-len(ext)]
					break
				}
			}
			if sum := firstHash(readSmall(filepath.Join(dir, name))); sum != "" {
				sidecars[normalizeRel(base)] = sum
			}
			continue
		}
		if checksumFileNames[lower] {
			for _, line := range strings.Split(readSmall(filepath.Join(dir, name)), "\n") {
				fields := strings.Fields(strings.TrimSpace(strings.TrimPrefix(line, "*")))
				if len(fields) < 2 {
					continue
				}
				sum := strings.ToLower(fields[0])
				if !isHex64(sum) {
					continue
				}
				target := strings.TrimPrefix(strings.TrimSpace(strings.Join(fields[1:], " ")), "*")
				target = strings.TrimPrefix(target, "./")
				manifests[strings.ToLower(filepath.Base(target))] = sum
			}
		}
	}
	return sidecars, manifests
}

func readSmall(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	var b strings.Builder
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if b.Len() > 4096 {
			break
		}
		b.WriteString(sc.Text())
		b.WriteByte('\n')
	}
	return b.String()
}

// firstHash returns the first 64 character hex token of s.
func firstHash(s string) string {
	for _, f := range strings.Fields(s) {
		f = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(f), "*"))
		if isHex64(f) {
			return f
		}
	}
	return ""
}

func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return true
}

func publishTime(dir, declared string) time.Time {
	if t, ok := parseTime(declared); ok {
		return t
	}
	if data := strings.TrimSpace(readSmall(filepath.Join(dir, publishedFile))); data != "" {
		if t, ok := parseTime(data); ok {
			return t
		}
	}
	if st, err := os.Stat(dir); err == nil {
		return st.ModTime()
	}
	return time.Time{}
}

func parseTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// readNotes returns the content of the first changelog file found.
func readNotes(dir string) string {
	for _, name := range notesCandidates {
		if data, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			return strings.TrimSpace(strings.ReplaceAll(string(data), "\r\n", "\n"))
		}
	}
	return ""
}

// resolveIcon finds the application icon: the declared file when it exists,
// otherwise the first conventional icon name.
func resolveIcon(dir, declared string) string {
	exists := func(name string) bool {
		if name == "" {
			return false
		}
		st, err := os.Stat(filepath.Join(dir, filepath.FromSlash(name)))
		return err == nil && !st.IsDir()
	}
	if exists(declared) {
		return declared
	}
	for _, name := range []string{"icon.png", "icon.svg", "logo.png", "logo.svg", "icon.jpg"} {
		if exists(name) {
			return name
		}
	}
	return ""
}

func isPrerelease(version string, declared *bool) bool {
	if declared != nil {
		return *declared
	}
	v := strings.TrimPrefix(strings.TrimPrefix(version, "v"), "V")
	return strings.ContainsAny(v, "-_")
}

// readJSON decodes an optional JSON file. A missing file is not a warning; a
// malformed one is reported and otherwise ignored.
func readJSON[T any](path string, snap *Snapshot) (T, bool) {
	var v T
	data, err := os.ReadFile(path)
	if err != nil {
		return v, false
	}
	if err := json.Unmarshal(data, &v); err != nil {
		snap.warn("%s: invalid JSON: %v", path, err)
		return v, false
	}
	return v, true
}

// normalizeRel cleans a manifest supplied relative path.
func normalizeRel(p string) string {
	p = strings.TrimSpace(filepath.ToSlash(p))
	if p == "" {
		return ""
	}
	p = filepath.ToSlash(filepath.Clean(filepath.FromSlash(p)))
	return strings.TrimPrefix(p, "./")
}

// validRel rejects empty and directory-escaping relative paths.
func validRel(p string) bool {
	return p != "" && p != "." && p != ".." &&
		!strings.HasPrefix(p, "../") && !filepath.IsAbs(filepath.FromSlash(p))
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}
