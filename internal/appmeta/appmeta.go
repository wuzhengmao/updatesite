// Package appmeta reads and writes the application level metadata that lives
// beside the release directories: app.json and the icon.
//
// Releases are published by unpacking an archive into <app>/<version>/, but
// app.json and the icon sit one level up and describe the application itself,
// so they get their own small read/modify/write path.
package appmeta

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wuzhengmao/updatesite/internal/index"
)

// FileName is the metadata file inside an application directory.
const FileName = "app.json"

// IconNames are the file names the scanner looks for, in priority order. An
// uploaded icon is written as the first name matching its type, and the others
// are removed so exactly one icon remains.
var IconNames = []string{"icon.png", "icon.svg", "logo.png", "logo.svg", "icon.jpg"}

// MaxIconBytes bounds an uploaded icon. Icons are small; anything larger is a
// mistake or an attack.
const MaxIconBytes = 2 << 20

// Field limits for the text metadata.
const (
	MaxShortField = 300
	MaxLongField  = 20000
	MaxTags       = 30
	MaxPlatforms  = 20
)

// writes serialises the read-modify-write cycles that touch app.json, so an
// icon upload and a metadata save cannot clobber each other.
var writes sync.Mutex

// Read returns the stored metadata. A missing app.json yields a zero value, so
// the caller can present an empty form rather than an error.
func Read(appsDir, appID string) (index.AppMeta, error) {
	if err := validateAppID(appID); err != nil {
		return index.AppMeta{}, err
	}
	var meta index.AppMeta
	data, err := os.ReadFile(filepath.Join(appsDir, appID, FileName))
	if os.IsNotExist(err) {
		return meta, nil
	}
	if err != nil {
		return meta, err
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return meta, fmt.Errorf("%s is not valid JSON: %w", FileName, err)
	}
	meta.ID = appID // the directory name is always authoritative
	return meta, nil
}

// Write validates and stores the metadata, creating the application directory
// when the application does not exist yet.
func Write(appsDir, appID string, meta index.AppMeta) error {
	if err := validateAppID(appID); err != nil {
		return err
	}
	if err := Validate(&meta); err != nil {
		return err
	}
	meta.ID = appID

	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	writes.Lock()
	defer writes.Unlock()
	return writeFile(appsDir, appID, FileName, data)
}

// Validate checks the field lengths and shapes so a bad form cannot store
// something the site would then choke on.
func Validate(meta *index.AppMeta) error {
	check := func(field, value string, max int) error {
		if len([]rune(value)) > max {
			return fmt.Errorf("%s 最多 %d 个字符", field, max)
		}
		return nil
	}
	for _, c := range []struct {
		name  string
		value string
		max   int
	}{
		{"name", meta.Name, MaxShortField},
		{"summary", meta.Summary, MaxShortField},
		{"vendor", meta.Vendor, MaxShortField},
		{"license", meta.License, MaxShortField},
		{"channel", meta.Channel, MaxShortField},
		{"icon", meta.Icon, MaxShortField},
		{"homepage", meta.Homepage, MaxShortField},
		{"description", meta.Description, MaxLongField},
	} {
		if err := check(c.name, c.value, c.max); err != nil {
			return err
		}
	}
	if meta.Homepage != "" && !looksLikeURL(meta.Homepage) {
		return fmt.Errorf("homepage 需要以 http:// 或 https:// 开头")
	}
	if len(meta.Tags) > MaxTags {
		return fmt.Errorf("tags 最多 %d 个", MaxTags)
	}
	for _, tag := range meta.Tags {
		if len([]rune(tag)) > 60 {
			return fmt.Errorf("单个 tag 最多 60 个字符")
		}
	}
	if len(meta.Platforms) > MaxPlatforms {
		return fmt.Errorf("platforms 最多 %d 个", MaxPlatforms)
	}
	if meta.Channel != "" {
		meta.Channel = index.NormalizeChannel(meta.Channel)
	}
	return nil
}

func looksLikeURL(s string) bool {
	lower := strings.ToLower(s)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

// ---------------------------------------------------------------- the icon

// AcceptedIcon lists the image types an icon may use, keyed by extension.
var AcceptedIcon = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".svg":  "image/svg+xml",
	".webp": "image/webp",
}

// WriteIcon stores an icon and points app.json at it.
//
// The file name is chosen from the sniffed content, never from anything the
// caller sent, so an upload cannot pick its own path or extension. Other icon
// files are removed so exactly one remains, and app.json.icon is set so the
// result is unambiguous even if a custom name was configured before.
func WriteIcon(appsDir, appID string, data []byte) (string, error) {
	if err := validateAppID(appID); err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", fmt.Errorf("图标内容为空")
	}
	if len(data) > MaxIconBytes {
		return "", fmt.Errorf("图标不能超过 %d KB", MaxIconBytes/1024)
	}
	ext, ok := sniffImage(data)
	if !ok {
		return "", fmt.Errorf("图标必须是 PNG、JPEG、SVG 或 WebP 图片")
	}
	name := "icon" + ext

	writes.Lock()
	defer writes.Unlock()

	if err := writeFile(appsDir, appID, name, data); err != nil {
		return "", err
	}
	// Remove the other accepted names so the scanner cannot pick a stale one.
	for _, other := range IconNames {
		if other == name {
			continue
		}
		os.Remove(filepath.Join(appsDir, appID, other))
	}

	meta, err := readLocked(appsDir, appID)
	if err != nil {
		return "", err
	}
	meta.Icon = name
	if err := writeMetaLocked(appsDir, appID, meta); err != nil {
		return "", err
	}
	return name, nil
}

// sniffImage identifies an image from its leading bytes.
func sniffImage(data []byte) (string, bool) {
	switch {
	case len(data) >= 8 && bytes.Equal(data[:8], []byte("\x89PNG\r\n\x1a\n")):
		return ".png", true
	case len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff:
		return ".jpg", true
	case len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return ".webp", true
	}
	if looksLikeSVG(data) {
		return ".svg", true
	}
	return "", false
}

func looksLikeSVG(data []byte) bool {
	head := data
	if len(head) > 4096 {
		head = head[:4096]
	}
	// Leading whitespace, an XML declaration or a byte order mark are all fine
	// before the root element; only a real HTML document is not.
	text := strings.TrimSpace(strings.ToLower(string(head)))
	// An <svg> root must appear before any other element.
	i := strings.Index(text, "<svg")
	if i < 0 {
		return false
	}
	for _, tag := range []string{"<html", "<script", "<!doctype html"} {
		if j := strings.Index(text, tag); j >= 0 && j < i {
			return false
		}
	}
	return true
}

// ----------------------------------------------------------------- plumbing

func validateAppID(appID string) error {
	switch {
	case appID == "":
		return fmt.Errorf("应用 ID 不能为空")
	case appID == "." || appID == "..":
		return fmt.Errorf("%q 不是合法的应用 ID", appID)
	case strings.HasPrefix(appID, "."):
		return fmt.Errorf("应用 ID 不能以点开头")
	case strings.ContainsAny(appID, `/\`):
		return fmt.Errorf("应用 ID 不能包含路径分隔符")
	case strings.ContainsRune(appID, 0):
		return fmt.Errorf("应用 ID 含有非法字符")
	}
	return nil
}

func readLocked(appsDir, appID string) (index.AppMeta, error) {
	var meta index.AppMeta
	data, err := os.ReadFile(filepath.Join(appsDir, appID, FileName))
	if os.IsNotExist(err) {
		return meta, nil
	}
	if err != nil {
		return meta, err
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		// A corrupt file must not block an icon upload; start from scratch.
		return index.AppMeta{}, nil
	}
	meta.ID = appID
	return meta, nil
}

func writeMetaLocked(appsDir, appID string, meta index.AppMeta) error {
	meta.ID = appID
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(appsDir, appID, FileName, append(data, '\n'))
}

// writeFile writes atomically: a temporary file in the same directory, then a
// rename, so a reader never sees a half written app.json.
func writeFile(appsDir, appID, name string, data []byte) error {
	dir := filepath.Join(appsDir, appID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("cannot create %s: %w (is the archive mounted read-write?)", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".appmeta-")
	if err != nil {
		return fmt.Errorf("cannot write in %s: %w (is the archive mounted read-write?)", dir, err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := replaceFile(tmpName, filepath.Join(dir, name)); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// replaceFile renames tmp over target, retrying briefly on failure.
//
// Windows refuses the rename while anything holds a handle on the target, which
// happens in practice: a virus scanner picking up the freshly created temporary
// file, the search indexer, or the background scanner reading app.json. Those
// holds last milliseconds, so a short retry turns a spurious failure into a
// success. The rename stays atomic either way.
func replaceFile(tmp, target string) error {
	var err error
	for attempt := 0; attempt < 6; attempt++ {
		if err = os.Rename(tmp, target); err == nil {
			return nil
		}
		time.Sleep(time.Duration(10*(attempt+1)) * time.Millisecond)
	}
	return err
}
