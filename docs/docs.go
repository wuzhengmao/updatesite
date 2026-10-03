// Package docs embeds the Markdown documentation so the running site can serve
// it. Keeping the files in this directory makes them the single source of
// truth: the same files are readable on disk and browsable at /docs.
package docs

import "embed"

// FS holds every Markdown file in this directory. Dropping a new .md file here
// is enough to make it appear on the site.
//
//go:embed *.md
var FS embed.FS
