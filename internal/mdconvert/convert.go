// Author: Emmanuel COLUSSI
// Copyright (c) 2026 Emmanuel COLUSSI
// SPDX-License-Identifier: MIT
//
// Package mdconvert converts Markdown with GFM extensions (tables, task lists,
// and strikethrough) to the XHTML storage format used by Confluence.
//
// Mermaid code blocks are rendered as vector SVG files through a
// MermaidRenderer and referenced as attachments. Local Markdown images are
// also collected for upload.
package mdconvert

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yuin/goldmark"
	gast "github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension"
	eastast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// MermaidRenderer renders a Mermaid diagram to an SVG file at destPath.
type MermaidRenderer interface {
	Render(source string, destSVG string) error
}

// Attachment describes a local file to upload and reference by filename.
type Attachment struct {
	Filename  string // name referenced by the page and attached to Confluence
	LocalPath string // source file path on disk
}

// Result contains the output of a conversion.
type Result struct {
	Storage     string
	Attachments []Attachment
	Warnings    []string
}

// Converter converts a Markdown document to Confluence storage format.
type Converter struct {
	// BaseDir resolves relative image paths from Markdown.
	BaseDir string
	// Mermaid enables image rendering for Mermaid blocks when non-nil.
	// A nil value renders Mermaid blocks as plain code.
	Mermaid MermaidRenderer
	// MermaidOutDir is the directory where generated SVG files are written.
	MermaidOutDir string
}

type renderState struct {
	c             *Converter
	source        []byte
	buf           bytes.Buffer
	attachments   []Attachment
	seenByPath    map[string]string // absolute path -> previously registered filename
	seenNames     map[string]bool
	warnings      []string
	taskIDCounter int
}

// Convert transforms Markdown into Confluence storage format.
func (c *Converter) Convert(md []byte) (*Result, error) {
	gm := goldmark.New(goldmark.WithExtensions(east.GFM))
	reader := text.NewReader(md)
	doc := gm.Parser().Parse(reader)

	st := &renderState{
		c:          c,
		source:     md,
		seenByPath: map[string]string{},
		seenNames:  map[string]bool{},
	}

	for n := doc.FirstChild(); n != nil; n = n.NextSibling() {
		st.renderBlock(n)
	}

	return &Result{
		Storage:     st.buf.String(),
		Attachments: st.attachments,
		Warnings:    st.warnings,
	}, nil
}

func (st *renderState) warn(format string, args ...any) {
	st.warnings = append(st.warnings, fmt.Sprintf(format, args...))
}

// registerAttachment registers a local file and returns a deduplicated
// filename for storage format.
func (st *renderState) registerAttachment(localPath, preferredName string) string {
	abs, err := filepath.Abs(localPath)
	if err != nil {
		abs = localPath
	}
	if name, ok := st.seenByPath[abs]; ok {
		return name
	}

	name := preferredName
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	candidate := name
	i := 1
	for st.seenNames[candidate] {
		i++
		candidate = fmt.Sprintf("%s-%d%s", base, i, ext)
	}

	st.seenNames[candidate] = true
	st.seenByPath[abs] = candidate
	st.attachments = append(st.attachments, Attachment{Filename: candidate, LocalPath: localPath})
	return candidate
}

func hashShort(s string) string {
	h := sha1.Sum([]byte(s))
	return hex.EncodeToString(h[:])[:10]
}

// ---- Blocs ----

func (st *renderState) renderBlock(n gast.Node) {
	switch v := n.(type) {
	case *gast.Heading:
		tag := fmt.Sprintf("h%d", v.Level)
		st.buf.WriteString("<" + tag + ">")
		st.renderInlineChildren(v)
		st.buf.WriteString("</" + tag + ">\n")

	case *gast.Paragraph:
		st.buf.WriteString("<p>")
		st.renderInlineChildren(v)
		st.buf.WriteString("</p>\n")

	case *gast.TextBlock:
		st.renderInlineChildren(v)

	case *gast.ThematicBreak:
		st.buf.WriteString("<hr/>\n")

	case *gast.Blockquote:
		st.buf.WriteString("<blockquote>\n")
		for c := v.FirstChild(); c != nil; c = c.NextSibling() {
			st.renderBlock(c)
		}
		st.buf.WriteString("</blockquote>\n")

	case *gast.CodeBlock:
		st.renderCodeMacro("", nodeLinesText(v, st.source))

	case *gast.FencedCodeBlock:
		lang := strings.ToLower(strings.TrimSpace(string(v.Language(st.source))))
		code := nodeLinesText(v, st.source)
		if lang == "mermaid" {
			st.renderMermaid(code)
		} else {
			st.renderCodeMacro(lang, code)
		}

	case *gast.List:
		if isTaskList(v) {
			st.renderTaskList(v)
		} else {
			tag := "ul"
			if v.IsOrdered() {
				tag = "ol"
			}
			st.buf.WriteString("<" + tag + ">\n")
			for c := v.FirstChild(); c != nil; c = c.NextSibling() {
				if li, ok := c.(*gast.ListItem); ok {
					st.renderListItem(li)
				}
			}
			st.buf.WriteString("</" + tag + ">\n")
		}

	case *eastast.Table:
		st.renderTable(v)

	case *gast.HTMLBlock:
		st.warn("raw HTML block ignored (not supported in this version)")

	default:
		// Walk children of unknown block types to avoid silently losing content.
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			st.renderBlock(c)
		}
	}
}

func (st *renderState) renderListItem(li *gast.ListItem) {
	st.buf.WriteString("<li>")
	first := true
	for c := li.FirstChild(); c != nil; c = c.NextSibling() {
		if p, ok := c.(*gast.Paragraph); ok {
			st.renderInlineChildren(p)
			first = false
			continue
		}
		if !first {
			st.buf.WriteString("\n")
		}
		st.renderBlock(c)
		first = false
	}
	st.buf.WriteString("</li>\n")
}

// firstIsCheckbox reports whether the first child of container (a Paragraph
// for a loose list or a TextBlock for a tight list) is a GFM task checkbox.
func firstIsCheckbox(container gast.Node) (*eastast.TaskCheckBox, bool) {
	if container == nil {
		return nil, false
	}
	if cb, ok := container.FirstChild().(*eastast.TaskCheckBox); ok {
		return cb, true
	}
	return nil, false
}

// isTaskList reports whether every list item starts with a GFM checkbox. Task
// lists are rendered as Confluence <ac:task-list> macros.
func isTaskList(v *gast.List) bool {
	found := false
	for c := v.FirstChild(); c != nil; c = c.NextSibling() {
		li, ok := c.(*gast.ListItem)
		if !ok {
			continue
		}
		if _, ok := firstIsCheckbox(li.FirstChild()); !ok {
			return false
		}
		found = true
	}
	return found
}

// renderTaskList converts a GFM task list to a Confluence task-list macro.
// <ac:task-list>.
func (st *renderState) renderTaskList(v *gast.List) {
	st.buf.WriteString("<ac:task-list>\n")
	for c := v.FirstChild(); c != nil; c = c.NextSibling() {
		li, ok := c.(*gast.ListItem)
		if !ok {
			continue
		}
		container := li.FirstChild()
		cb, ok := firstIsCheckbox(container)
		if !ok {
			continue
		}
		st.taskIDCounter++
		status := "incomplete"
		if cb.IsChecked {
			status = "complete"
		}
		st.buf.WriteString(fmt.Sprintf(`<ac:task><ac:task-id>%d</ac:task-id><ac:task-status>%s</ac:task-status><ac:task-body>`, st.taskIDCounter, status))
		for ic := cb.NextSibling(); ic != nil; ic = ic.NextSibling() {
			st.renderInline(ic)
		}
		st.buf.WriteString(`</ac:task-body></ac:task>` + "\n")

		// Render additional item blocks, such as nested lists, after the
		// checkbox container.
		for extra := container.NextSibling(); extra != nil; extra = extra.NextSibling() {
			st.renderBlock(extra)
		}
	}
	st.buf.WriteString("</ac:task-list>\n")
}

func (st *renderState) renderCodeMacro(lang, code string) {
	st.buf.WriteString(`<ac:structured-macro ac:name="code" ac:schema-version="1">`)
	if lang != "" {
		st.buf.WriteString(`<ac:parameter ac:name="language">` + confluenceLang(lang) + `</ac:parameter>`)
	}
	st.buf.WriteString(`<ac:parameter ac:name="linenumbers">true</ac:parameter>`)
	st.buf.WriteString(`<ac:plain-text-body><![CDATA[`)
	st.buf.WriteString(strings.ReplaceAll(code, "]]>", "]]]]><![CDATA[>"))
	st.buf.WriteString(`]]></ac:plain-text-body></ac:structured-macro>` + "\n")
}

var langAliases = map[string]string{
	"js":         "javascript",
	"ts":         "javascript",
	"typescript": "javascript",
	"golang":     "go",
	"sh":         "bash",
	"shell":      "bash",
	"zsh":        "bash",
	"yml":        "yaml",
	"c++":        "cpp",
	"c#":         "csharp",
	"objectivec": "objc",
	"md":         "none",
	"text":       "none",
	"":           "none",
}

func confluenceLang(lang string) string {
	if v, ok := langAliases[lang]; ok {
		return v
	}
	return lang
}

func (st *renderState) renderMermaid(code string) {
	code = strings.TrimSpace(code)
	if code == "" {
		return
	}
	if st.c.Mermaid == nil {
		st.warn("Mermaid block rendered as plain text (no MermaidRenderer configured)")
		st.renderCodeMacro("none", code)
		return
	}

	name := "mermaid-" + hashShort(code) + ".svg"
	outDir := st.c.MermaidOutDir
	if outDir == "" {
		outDir = os.TempDir()
	}
	dest := filepath.Join(outDir, name)

	if err := st.c.Mermaid.Render(code, dest); err != nil {
		st.warn("Mermaid rendering failed (%s); diagram inserted as a code block: %v", hashShort(code), err)
		st.renderCodeMacro("none", code)
		return
	}

	filename := st.registerAttachment(dest, name)
	st.buf.WriteString(`<ac:image><ri:attachment ri:filename="` + escapeXML(filename) + `"/></ac:image>` + "\n")
}

func (st *renderState) renderTable(t *eastast.Table) {
	st.buf.WriteString("<table><tbody>\n")
	for row := t.FirstChild(); row != nil; row = row.NextSibling() {
		switch r := row.(type) {
		case *eastast.TableHeader:
			st.buf.WriteString("<tr>")
			for cell := r.FirstChild(); cell != nil; cell = cell.NextSibling() {
				if tc, ok := cell.(*eastast.TableCell); ok {
					st.buf.WriteString("<th>")
					st.renderInlineChildren(tc)
					st.buf.WriteString("</th>")
				}
			}
			st.buf.WriteString("</tr>\n")
		case *eastast.TableRow:
			st.buf.WriteString("<tr>")
			for cell := r.FirstChild(); cell != nil; cell = cell.NextSibling() {
				if tc, ok := cell.(*eastast.TableCell); ok {
					st.buf.WriteString("<td>")
					st.renderInlineChildren(tc)
					st.buf.WriteString("</td>")
				}
			}
			st.buf.WriteString("</tr>\n")
		}
	}
	st.buf.WriteString("</tbody></table>\n")
}

// ---- Inline ----

func (st *renderState) renderInlineChildren(n gast.Node) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		st.renderInline(c)
	}
}

func (st *renderState) renderInline(n gast.Node) {
	switch v := n.(type) {
	case *gast.Text:
		st.buf.WriteString(escapeXML(string(v.Value(st.source))))
		// Treat a soft source newline as a visible line break. This matches the
		// expectations of users writing Markdown manually or pasting from Word.
		if v.SoftLineBreak() || v.HardLineBreak() {
			st.buf.WriteString("<br/>\n")
		}

	case *gast.String:
		st.buf.WriteString(escapeXML(string(v.Value)))

	case *gast.CodeSpan:
		st.buf.WriteString("<code>")
		for c := v.FirstChild(); c != nil; c = c.NextSibling() {
			if t, ok := c.(*gast.Text); ok {
				st.buf.WriteString(escapeXML(string(t.Value(st.source))))
			}
		}
		st.buf.WriteString("</code>")

	case *gast.Emphasis:
		tag := "em"
		if v.Level == 2 {
			tag = "strong"
		}
		st.buf.WriteString("<" + tag + ">")
		st.renderInlineChildren(v)
		st.buf.WriteString("</" + tag + ">")

	case *eastast.Strikethrough:
		st.buf.WriteString("<s>")
		st.renderInlineChildren(v)
		st.buf.WriteString("</s>")

	case *gast.AutoLink:
		url := string(v.URL(st.source))
		st.buf.WriteString(`<a href="` + escapeXMLAttr(url) + `">` + escapeXML(url) + `</a>`)

	case *gast.Link:
		st.buf.WriteString(`<a href="` + escapeXMLAttr(string(v.Destination)) + `">`)
		st.renderInlineChildren(v)
		st.buf.WriteString(`</a>`)

	case *gast.Image:
		st.renderImage(v)

	case *gast.RawHTML:
		st.warn("raw inline HTML ignored")

	case *eastast.TaskCheckBox:
		// Normally handled by renderTaskListItem. A checkbox elsewhere in an
		// item is displayed as text.
		if v.IsChecked {
			st.buf.WriteString("☑ ")
		} else {
			st.buf.WriteString("☐ ")
		}

	default:
		st.renderInlineChildren(n)
	}
}

func (st *renderState) renderImage(v *gast.Image) {
	dest := string(v.Destination)
	if strings.HasPrefix(dest, "http://") || strings.HasPrefix(dest, "https://") {
		st.buf.WriteString(`<ac:image><ri:url ri:value="` + escapeXMLAttr(dest) + `"/></ac:image>`)
		return
	}

	local := dest
	if !filepath.IsAbs(local) && st.c.BaseDir != "" {
		local = filepath.Join(st.c.BaseDir, local)
	}
	if _, err := os.Stat(local); err != nil {
		st.warn("local image not found, ignored: %s", dest)
		st.buf.WriteString(`<p><em>[missing image: ` + escapeXML(dest) + `]</em></p>`)
		return
	}

	filename := st.registerAttachment(local, filepath.Base(local))
	st.buf.WriteString(`<ac:image><ri:attachment ri:filename="` + escapeXML(filename) + `"/></ac:image>`)
}

// ---- Utils ----

func nodeLinesText(n gast.Node, source []byte) string {
	var buf bytes.Buffer
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		buf.Write(seg.Value(source))
	}
	return buf.String()
}

func escapeXML(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
	)
	return r.Replace(s)
}

func escapeXMLAttr(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
	)
	return r.Replace(s)
}
