package server

import (
	"html/template"
	"io/fs"
	"net/http"
	"sort"
	"strings"

	"github.com/mti/updatesite/docs"
)

// docOrder lists the documents that should be shown first, in order. Any other
// Markdown file in the docs directory is appended alphabetically.
var docOrder = []string{"RELEASE-SPEC.md", "API.md"}

// doc is one rendered documentation page.
type doc struct {
	Slug  string // URL segment, e.g. "release-spec"
	File  string // source file name, e.g. "RELEASE-SPEC.md"
	Title string
	Body  template.HTML
}

// docLink is one entry of the documentation sidebar.
type docLink struct {
	Slug   string
	Title  string
	Active bool
}

// loadDocs reads and renders every embedded Markdown document.
func loadDocs() ([]*doc, error) {
	entries, err := fs.ReadDir(docs.FS, ".")
	if err != nil {
		return nil, err
	}

	rank := func(name string) (int, string) {
		for i, want := range docOrder {
			if strings.EqualFold(want, name) {
				return i, ""
			}
		}
		return len(docOrder), strings.ToLower(name)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && hasSuffixFold(e.Name(), ".md") {
			names = append(names, e.Name())
		}
	}
	sort.Slice(names, func(i, j int) bool {
		ri, ni := rank(names[i])
		rj, nj := rank(names[j])
		if ri != rj {
			return ri < rj
		}
		return ni < nj
	})

	out := make([]*doc, 0, len(names))
	for _, name := range names {
		data, err := fs.ReadFile(docs.FS, name)
		if err != nil {
			return nil, err
		}
		text := strings.ReplaceAll(string(data), "\r\n", "\n")
		out = append(out, &doc{
			Slug:  slugify(name[:len(name)-3]), // strip ".md"
			File:  name,
			Title: docTitle(text, name),
			Body:  RenderMarkdown(text),
		})
	}
	return out, nil
}

// docTitle uses the file's first level-one heading, falling back to its name.
func docTitle(text, fallback string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, " \t")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if m := reHeading.FindStringSubmatch(line); m != nil && len(m[1]) == 1 {
			if title := strings.TrimSpace(m[2]); title != "" {
				return title
			}
		}
		break // only the first non-blank line can be the title
	}
	return strings.TrimSuffix(fallback, ".md")
}

// slugify lower-cases a file name stem and replaces runs of separators with a
// single dash, so "RELEASE-SPEC" becomes "release-spec".
func slugify(stem string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(stem) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func hasSuffixFold(s, suffix string) bool {
	return len(s) >= len(suffix) && strings.EqualFold(s[len(s)-len(suffix):], suffix)
}

// docsLinks builds the sidebar entries, marking the current page.
func docsLinks(all []*doc, current string) []docLink {
	out := make([]docLink, 0, len(all))
	for _, d := range all {
		out = append(out, docLink{Slug: d.Slug, Title: d.Title, Active: d.Slug == current})
	}
	return out
}

func (s *Server) findDoc(slug string) *doc {
	if slug == "" {
		if len(s.docs) == 0 {
			return nil
		}
		return s.docs[0]
	}
	for _, d := range s.docs {
		if strings.EqualFold(d.Slug, slug) || strings.EqualFold(d.File, slug) {
			return d
		}
	}
	return nil
}

func (s *Server) handleDocs(w http.ResponseWriter, r *http.Request) {
	s.renderDoc(w, r, "")
}

func (s *Server) handleDoc(w http.ResponseWriter, r *http.Request) {
	s.renderDoc(w, r, r.PathValue("name"))
}

func (s *Server) renderDoc(w http.ResponseWriter, r *http.Request, slug string) {
	if len(s.docs) == 0 {
		s.renderError(w, r, http.StatusNotFound, "站点没有内置任何文档。")
		return
	}
	d := s.findDoc(slug)
	if d == nil {
		s.renderError(w, r, http.StatusNotFound, "找不到这个文档页。")
		return
	}
	s.render(w, r, "docs", pageData{
		Site:  s.site(),
		Title: d.Title,
		Docs:  docsLinks(s.docs, d.Slug),
		Doc:   d,
	})
}
