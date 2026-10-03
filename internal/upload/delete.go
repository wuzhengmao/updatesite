package upload

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wuzhengmao/updatesite/internal/index"
)

// ErrNotFound reports that the application or version to remove is not there.
var ErrNotFound = errors.New("not found")

// trashPrefix marks a directory that is on its way out. The scanner skips
// leading dots, so a release stops being visible the moment it is parked.
const trashPrefix = ".trash-"

// DeleteVersion removes one published version of an application.
//
// The directory is renamed out of the way first and only then erased. That
// ordering means the release disappears from the site at once and cannot be
// left half deleted if the erase fails part way — which happens on Windows
// whenever something still holds a file open.
func DeleteVersion(appsDir, appID, version string) error {
	if err := ValidateAppID(appID); err != nil {
		return err
	}
	appDir := filepath.Join(appsDir, appID)
	if err := validateVersionName(appDir, version); err != nil {
		return err
	}
	target := filepath.Join(appDir, version)
	if !isWithin(appDir, target) {
		return fmt.Errorf("%q escapes the application directory", version)
	}
	if st, err := os.Stat(target); err != nil || !st.IsDir() {
		return fmt.Errorf("版本 %s %w", version, ErrNotFound)
	}
	return discard(appDir, target)
}

// DeleteApp removes an application entirely: every version, app.json and the
// icon.
func DeleteApp(appsDir, appID string) error {
	if err := ValidateAppID(appID); err != nil {
		return err
	}
	target := filepath.Join(appsDir, appID)
	if !isWithin(appsDir, target) {
		return fmt.Errorf("%q escapes the archive directory", appID)
	}
	if st, err := os.Stat(target); err != nil || !st.IsDir() {
		return fmt.Errorf("应用 %s %w", appID, ErrNotFound)
	}
	return discard(appsDir, target)
}

// discard parks a directory under a hidden name and then erases it.
func discard(parent, target string) error {
	parked := filepath.Join(parent, trashPrefix+randomSuffix())
	if err := renameWithRetry(target, parked); err != nil {
		return fmt.Errorf("cannot remove %s: %w", target, err)
	}
	// From here the directory is already invisible to the scanner, so a failure
	// to erase it is untidy rather than harmful.
	if err := os.RemoveAll(parked); err != nil {
		return fmt.Errorf("removed %s from the site, but could not erase %s: %w",
			filepath.Base(target), parked, err)
	}
	return nil
}

// validateVersionName accepts anything the scanner would have published: a
// version shaped directory name, or any directory that carries a release.json.
// Everything else is refused, which keeps a crafted name from reaching outside
// the application directory.
func validateVersionName(appDir, name string) error {
	switch {
	case name == "":
		return fmt.Errorf("版本号不能为空")
	case name == "." || name == "..":
		return fmt.Errorf("%q 不是合法的版本号", name)
	case strings.HasPrefix(name, "."):
		return fmt.Errorf("版本号不能以点开头")
	case strings.ContainsAny(name, `/\`):
		return fmt.Errorf("版本号不能包含路径分隔符")
	}
	if index.IsVersionDir(name) {
		return nil
	}
	// A directory with a release.json is accepted by the scanner whatever it is
	// called, so removing it must work too.
	if st, err := os.Stat(filepath.Join(appDir, name, releaseMetaName)); err == nil && !st.IsDir() {
		return nil
	}
	return fmt.Errorf("%q 不是已发布的版本号", name)
}

// releaseMetaName mirrors the file the scanner looks for.
const releaseMetaName = "release.json"

// isWithin reports whether path stays inside dir.
func isWithin(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
