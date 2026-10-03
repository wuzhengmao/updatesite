package upload

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seed publishes two versions so the delete tests have something to remove.
func seed(t *testing.T) Options {
	t.Helper()
	opts := newOpts(t, "myapp", "1.0.0")
	if _, err := publish(t, zipOf(t, map[string]string{
		"App-1.0.0-windows-x64.exe": "one",
	}), FormatZip, opts); err != nil {
		t.Fatal(err)
	}
	opts.Version = "2.0.0"
	if _, err := publish(t, zipOf(t, map[string]string{
		"App-2.0.0-windows-x64.exe": "two",
		"CHANGELOG.md":              "notes",
	}), FormatZip, opts); err != nil {
		t.Fatal(err)
	}
	return opts
}

func TestDeleteVersion(t *testing.T) {
	opts := seed(t)
	appDir := filepath.Join(opts.AppsDir, "myapp")

	if err := DeleteVersion(opts.AppsDir, "myapp", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(appDir, "1.0.0")); !os.IsNotExist(err) {
		t.Error("the version directory is still there")
	}
	// The other version must be untouched.
	if _, err := os.Stat(filepath.Join(appDir, "2.0.0", "App-2.0.0-windows-x64.exe")); err != nil {
		t.Errorf("the sibling version was disturbed: %v", err)
	}
	// Nothing parked or left behind.
	assertNoLeftovers(t, appDir)
}

func TestDeleteMissingVersion(t *testing.T) {
	opts := seed(t)
	err := DeleteVersion(opts.AppsDir, "myapp", "9.9.9")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestDeleteVersionRejectsBadNames(t *testing.T) {
	opts := seed(t)
	for _, name := range []string{
		"", ".", "..", "../..", "../../etc", "a/b", `a\b`, ".hidden",
		"latest", // not a directory name the scanner would publish
	} {
		err := DeleteVersion(opts.AppsDir, "myapp", name)
		if err == nil {
			t.Errorf("DeleteVersion(%q) succeeded", name)
			continue
		}
		if errors.Is(err, ErrNotFound) && name == "latest" {
			// "latest" is a directory name lookup like any other; it simply is
			// not there. Either refusal is acceptable as long as nothing is
			// removed.
			continue
		}
	}
	// Both real versions are still present.
	for _, v := range []string{"1.0.0", "2.0.0"} {
		if _, err := os.Stat(filepath.Join(opts.AppsDir, "myapp", v)); err != nil {
			t.Errorf("version %s was removed by a rejected call: %v", v, err)
		}
	}
	// Nothing escaped the application directory.
	if _, err := os.Stat(filepath.Join(opts.AppsDir, "..", "etc")); err == nil {
		t.Error("a traversal path was created")
	}
}

// A directory carrying release.json is published by the scanner whatever it is
// called, so removing it has to work too.
func TestDeleteVersionAcceptsReleaseJSONDirectory(t *testing.T) {
	opts := newOpts(t, "myapp", "")
	if _, err := publish(t, zipOf(t, map[string]string{
		"release.json":        `{"version":"3.0.0"}`,
		"App-windows-x64.exe": "x",
	}), FormatZip, opts); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(opts.AppsDir, "myapp", "3.0.0")); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	if err := DeleteVersion(opts.AppsDir, "myapp", "3.0.0"); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteApp(t *testing.T) {
	opts := seed(t)
	appDir := filepath.Join(opts.AppsDir, "myapp")
	// Give the app some metadata as well; deleting must take that too.
	if err := os.WriteFile(filepath.Join(appDir, "app.json"), []byte(`{"name":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := DeleteApp(opts.AppsDir, "myapp"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(appDir); !os.IsNotExist(err) {
		t.Error("the application directory is still there")
	}
	assertNoLeftovers(t, opts.AppsDir)
}

func TestDeleteMissingApp(t *testing.T) {
	opts := seed(t)
	if err := DeleteApp(opts.AppsDir, "nosuchapp"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestDeleteAppRejectsBadIDs(t *testing.T) {
	opts := seed(t)
	for _, id := range []string{"", ".", "..", "../myapp", "a/b", `a\b`, ".hidden"} {
		if err := DeleteApp(opts.AppsDir, id); err == nil {
			t.Errorf("DeleteApp(%q) succeeded", id)
		}
	}
	if _, err := os.Stat(filepath.Join(opts.AppsDir, "myapp", "2.0.0")); err != nil {
		t.Errorf("a rejected call removed something: %v", err)
	}
}

// assertNoLeftovers checks that no parked or staging directory survived.
func assertNoLeftovers(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Errorf("leftover %q in %s", e.Name(), dir)
		}
	}
}
