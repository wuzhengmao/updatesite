package upload

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/wuzhengmao/updatesite/internal/index"
)

// Options describes one publish operation.
type Options struct {
	AppsDir string // <DATA_DIR>/apps
	AppID   string
	Version string // optional; resolved from the archive when empty
	Limits  Limits
}

// Result reports what a publish operation wrote.
type Result struct {
	App      string `json:"app"`
	Version  string `json:"version"`
	Replaced bool   `json:"replaced"`
	Files    []File `json:"files"`
	Bytes    int64  `json:"bytes"`
}

// ValidateAppID rejects ids that are not a single safe path component.
func ValidateAppID(id string) error {
	switch {
	case id == "":
		return fmt.Errorf("application id is empty")
	case id == "." || id == "..":
		return fmt.Errorf("%q is not a valid application id", id)
	case strings.HasPrefix(id, "."):
		return fmt.Errorf("application id must not start with a dot")
	case strings.ContainsAny(id, `/\`):
		return fmt.Errorf("application id must not contain a path separator")
	case strings.ContainsRune(id, 0):
		return fmt.Errorf("application id contains a NUL byte")
	}
	return nil
}

// Publish unpacks an archive and installs it as a release of one application.
//
// The archive is unpacked into a hidden staging directory next to the release
// directories, so the scanner never sees a half-written release. The finished
// directory is then swapped into place, replacing any existing release of the
// same version.
func Publish(archive io.ReaderAt, size int64, format Format, opts Options) (*Result, error) {
	if err := ValidateAppID(opts.AppID); err != nil {
		return nil, err
	}
	if opts.Limits.MaxEntries == 0 {
		opts.Limits = DefaultLimits
	}

	appDir := filepath.Join(opts.AppsDir, opts.AppID)
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		return nil, &NotWritableError{Path: appDir, Err: err}
	}

	staging, err := os.MkdirTemp(appDir, ".staging-")
	if err != nil {
		return nil, &NotWritableError{Path: appDir, Err: err}
	}
	// Any failure past this point must not leave a staging directory behind.
	committed := false
	defer func() {
		if !committed {
			os.RemoveAll(staging)
		}
	}()

	payload := filepath.Join(staging, "payload")
	files, err := Extract(archive, size, format, payload, opts.Limits)
	if err != nil {
		return nil, err
	}

	version, root, err := resolveVersion(payload, opts.Version, files)
	if err != nil {
		return nil, err
	}
	if err := validateVersion(version); err != nil {
		return nil, err
	}

	kept := make([]File, 0, len(files))
	for _, f := range files {
		rel, ok := relativeTo(root, payload, f.Path)
		if !ok {
			continue
		}
		kept = append(kept, File{Path: rel, Size: f.Size})
	}
	if err := checkHasArtifacts(kept); err != nil {
		return nil, err
	}

	target := filepath.Join(appDir, version)
	replaced, err := swapIntoPlace(root, target)
	if err != nil {
		return nil, err
	}
	committed = true
	os.RemoveAll(staging)

	res := &Result{App: opts.AppID, Version: version, Replaced: replaced, Files: kept}
	for _, f := range kept {
		res.Bytes += f.Size
	}
	return res, nil
}

// resolveVersion works out which directory is the release and what it is
// called. Precedence:
//
//  1. an explicit version (form field or query parameter)
//  2. a single top level directory named like a version
//  3. the "version" field of a release.json at the archive root
func resolveVersion(payload, explicit string, files []File) (version, root string, err error) {
	if explicit != "" {
		return explicit, payload, nil
	}

	// An archive made from a release folder usually has one top level
	// directory. Noise such as .DS_Store was already dropped during extraction.
	if dir, ok := singleTopDir(files); ok {
		if v, ok := versionFromDirName(dir); ok {
			return v, filepath.Join(payload, dir), nil
		}
	}

	if v := versionFromManifest(payload); v != "" {
		return v, payload, nil
	}
	return "", "", fmt.Errorf("cannot tell which version this is: pass a version field, " +
		"put the files in a single directory named after the version, or add a release.json with a version")
}

// versionFromDirName reads a version out of a directory name. It accepts a bare
// version ("1.2.0") and the GitHub style wrapper ("myapp-1.2.0",
// "App-1.2.3-rc.1"), where the version is the trailing part.
//
// Separators are tried from the right so that a product name containing dashes
// does not swallow the version: "app-v2-1.0.0" yields "1.0.0".
func versionFromDirName(name string) (string, bool) {
	if index.IsVersionDir(name) {
		return name, true
	}
	for i := len(name) - 1; i > 0; i-- {
		if name[i] != '-' && name[i] != '_' {
			continue
		}
		if candidate := name[i+1:]; index.IsVersionDir(candidate) {
			return candidate, true
		}
	}
	return "", false
}

// singleTopDir reports the name of the only top level directory, if the archive
// holds exactly that one directory and nothing else.
func singleTopDir(files []File) (string, bool) {
	top := ""
	for _, f := range files {
		i := strings.IndexByte(f.Path, '/')
		if i < 0 {
			return "", false // a loose file at the root
		}
		name := f.Path[:i]
		if top == "" {
			top = name
		} else if top != name {
			return "", false
		}
	}
	return top, top != ""
}

func versionFromManifest(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "release.json"))
	if err != nil {
		return ""
	}
	var meta struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(data, &meta) != nil {
		return ""
	}
	return strings.TrimSpace(meta.Version)
}

// validateVersion refuses a version that could not live on disk, or that would
// be ignored by the scanner.
func validateVersion(version string) error {
	if !index.IsVersionDir(version) {
		return fmt.Errorf("%q is not a valid version, expected something like 1.2.0 or 2.0.0-rc.1", version)
	}
	return nil
}

// checkHasArtifacts makes sure the upload carries something installable rather
// than only metadata files, which would produce an empty release.
func checkHasArtifacts(files []File) error {
	if len(files) == 0 {
		return fmt.Errorf("the archive is empty")
	}
	for _, f := range files {
		if !index.IsMetadataFile(f.Path) {
			return nil
		}
	}
	return fmt.Errorf("the archive holds no installable files, only metadata")
}

// relativeTo strips the payload root prefix from an extracted path.
func relativeTo(root, payload, p string) (string, bool) {
	if root == payload {
		return p, true
	}
	prefix := strings.TrimPrefix(root, payload+string(filepath.Separator))
	prefix = filepath.ToSlash(prefix)
	rest, ok := strings.CutPrefix(p, prefix+"/")
	return rest, ok
}

// swapIntoPlace moves root to target, replacing an existing directory. The old
// directory is parked under a hidden name first, so a failure can be undone.
func swapIntoPlace(root, target string) (replaced bool, err error) {
	var parked string
	if _, statErr := os.Stat(target); statErr == nil {
		parked = filepath.Join(filepath.Dir(target), ".trash-"+randomSuffix())
		if err := os.Rename(target, parked); err != nil {
			return false, fmt.Errorf("cannot replace %s: %w", target, err)
		}
		replaced = true
	}

	if err := os.Rename(root, target); err != nil {
		if parked != "" {
			// Put the previous release back rather than losing it.
			if restoreErr := os.Rename(parked, target); restoreErr != nil {
				return false, fmt.Errorf("cannot install %s (%v), and the previous release is left at %s",
					target, err, parked)
			}
		}
		return false, fmt.Errorf("cannot install %s: %w", target, err)
	}
	if parked != "" {
		if err := os.RemoveAll(parked); err != nil {
			// The new release is in place, so this is only untidy, not fatal.
			return replaced, nil
		}
	}
	return replaced, nil
}

func randomSuffix() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", os.Getpid())
	}
	return hex.EncodeToString(b[:])
}
