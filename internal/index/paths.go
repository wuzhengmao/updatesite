package index

import (
	"net/url"
	"strings"
)

// EscapePath percent-encodes every segment of a "/"-separated relative path.
func EscapePath(rel string) string {
	parts := strings.Split(rel, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// DownloadPath builds the canonical download URL path of an artifact. It is
// relative to the site root; callers prepend Config.BaseURL when they need an
// absolute URL.
func DownloadPath(app, version, file string) string {
	return "/dl/" + url.PathEscape(app) + "/" + url.PathEscape(version) + "/" + EscapePath(file)
}
