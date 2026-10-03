package server

import (
	"strings"
	"testing"
)

// md renders Markdown the way a changelog does.
func md(t *testing.T, src string) string {
	t.Helper()
	return string(RenderMarkdown(src))
}

func TestRenderMarkdownTable(t *testing.T) {
	got := md(t, "| 名称 | 说明 | 备注 |\n| :--- | ---: | :---: |\n| `a` | **粗** | 值 |\n| b | 2 |\n")

	for _, want := range []string{
		`<div class="table-wrap"><table>`,
		"<thead><tr>",
		`<th style="text-align:left">名称</th>`,
		`<th style="text-align:right">说明</th>`,
		`<th style="text-align:center">备注</th>`,
		"<tbody>",
		`<td style="text-align:left"><code>a</code></td>`,
		`<td style="text-align:right"><strong>粗</strong></td>`,
		"</tbody></table></div>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("table output missing %q\n--- got ---\n%s", want, got)
		}
	}

	// A short row is padded, so the column count always matches the header.
	if strings.Count(got, "<tr>") != 3 {
		t.Errorf("expected 1 header row + 2 body rows, got %d <tr>", strings.Count(got, "<tr>"))
	}
	if strings.Count(got, "<td") != 6 {
		t.Errorf("expected the 2-cell row to be padded to 3, got %d <td>", strings.Count(got, "<td"))
	}
}

// A horizontal rule or setext underline must not be swallowed as a table.
func TestRenderMarkdownRuleIsNotATable(t *testing.T) {
	for _, src := range []string{
		"some text | more text\n---\n",
		"标题\n===\n",
		"a | b\n***\n",
	} {
		got := md(t, src)
		if strings.Contains(got, "<table") {
			t.Errorf("RenderMarkdown(%q) produced a table:\n%s", src, got)
		}
	}
	if got := md(t, "some text | more\n---\n"); !strings.Contains(got, "<hr>") {
		t.Errorf("expected a rule, got:\n%s", got)
	}
}

func TestRenderMarkdownSingleColumnTable(t *testing.T) {
	got := md(t, "| 项目 |\n| --- |\n| 甲 |\n")
	if !strings.Contains(got, "<table>") || !strings.Contains(got, "<td>甲</td>") {
		t.Errorf("single column table not rendered:\n%s", got)
	}
}

func TestRenderMarkdownTableStopsAtBlankLine(t *testing.T) {
	got := md(t, "| a | b |\n| --- | --- |\n| 1 | 2 |\n\n普通段落\n")
	if !strings.Contains(got, "<td>1</td>") {
		t.Errorf("row not rendered:\n%s", got)
	}
	if !strings.Contains(got, "<p>普通段落</p>") {
		t.Errorf("paragraph after the table was swallowed:\n%s", got)
	}
}

// Everything inside a cell goes through the same escaping as body text.
func TestRenderMarkdownTableEscapes(t *testing.T) {
	got := md(t, "| x |\n| --- |\n| <img src=x onerror=alert(1)> |\n")
	if strings.Contains(got, "<img") {
		t.Errorf("raw HTML leaked through a table cell:\n%s", got)
	}
	if !strings.Contains(got, "&lt;img") {
		t.Errorf("cell content was not escaped:\n%s", got)
	}
}

func TestRenderMarkdownBlockBasics(t *testing.T) {
	got := md(t, "# 标题\n\n- 甲\n- 乙\n\n1. 一\n\n> 引用\n\n```json\n{\"a\": 1}\n```\n")
	for _, want := range []string{
		"<h1>标题</h1>", "<ul>", "<li>甲</li>", "<ol>", "<li>一</li>",
		"<blockquote>引用</blockquote>",
		`<pre><code>{&#34;a&#34;: 1}</code></pre>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestDocLinkResolver(t *testing.T) {
	for in, want := range map[string]string{
		"docs/RELEASE-SPEC.md":     "/docs/release-spec",
		"RELEASE-SPEC.md":          "/docs/release-spec",
		"./API.md":                 "/docs/api",
		"/docs/api":                "/docs/api",
		"#section":                 "#section",
		"https://example.com/a.md": "https://example.com/a.md",
		"mailto:x@y.md":            "mailto:x@y.md",
		"sub/dir/file.md":          "sub/dir/file.md",
		"plain.txt":                "plain.txt",
	} {
		if got := docLinkResolver(in); got != want {
			t.Errorf("docLinkResolver(%q) = %q, want %q", in, got, want)
		}
	}
}

// Relative Markdown links inside a document are rewritten, but a changelog is
// left alone so it cannot accidentally point at the documentation.
func TestRenderDocRewritesLinks(t *testing.T) {
	got := string(renderDoc("see [规范](docs/RELEASE-SPEC.md)"))
	if !strings.Contains(got, `href="/docs/release-spec"`) {
		t.Errorf("link was not rewritten:\n%s", got)
	}
	changelog := string(RenderMarkdown("see [规范](docs/RELEASE-SPEC.md)"))
	if strings.Contains(changelog, "href=") {
		t.Errorf("a changelog link should have been dropped, not rewritten:\n%s", changelog)
	}
}

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{
		"RELEASE-SPEC":  "release-spec",
		"API":           "api",
		"release_notes": "release-notes",
		"a  b":          "a-b",
		"---x---":       "x",
		"":              "",
	} {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDocTitle(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"# 应用发布规范 v1\n\n正文", "应用发布规范 v1"},
		{"\n\n# REST API\n", "REST API"},
		{"## 二级开头\n", "FALLBACK"},
		{"没有标题\n", "FALLBACK"},
	} {
		if got := docTitle(c.src, "FALLBACK.md"); got != c.want {
			t.Errorf("docTitle(%q) = %q, want %q", c.src, got, c.want)
		}
	}
}
