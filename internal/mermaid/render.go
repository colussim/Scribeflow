// Author: Emmanuel COLUSSI
// Copyright (c) 2026 Emmanuel COLUSSI
// SPDX-License-Identifier: MIT
//
// Package mermaid renders Mermaid diagrams as vector SVG by driving a local
// headless Chrome or Chromium process and reading its rendered DOM.
//
// Mermaid.js produces a content-fitted SVG directly, so rasterization and
// cropping are unnecessary. The embedded Mermaid.js asset keeps rendering
// local and offline; only an installed Chromium-based browser is required.
package mermaid

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

//go:embed assets/mermaid.min.js
var mermaidJS []byte

// Renderer converts Mermaid source into SVG files.
type Renderer struct {
	chromePath string
	workDir    string
	jsPath     string
	Theme      string        // "default", "dark", "neutral", "forest"...
	Timeout    time.Duration // timeout per diagram
}

// NewRenderer prepares a Renderer. An empty chromePath enables browser detection.
func NewRenderer(chromePath string) (*Renderer, error) {
	if chromePath == "" {
		var err error
		chromePath, err = findChrome()
		if err != nil {
			return nil, err
		}
	} else if _, err := os.Stat(chromePath); err != nil {
		return nil, fmt.Errorf("invalid Chrome path %q: %w", chromePath, err)
	}

	dir, err := os.MkdirTemp("", "confluence-publish-mermaid-*")
	if err != nil {
		return nil, fmt.Errorf("create temporary directory: %w", err)
	}

	jsPath := filepath.Join(dir, "mermaid.min.js")
	if err := os.WriteFile(jsPath, mermaidJS, 0o644); err != nil {
		return nil, fmt.Errorf("write mermaid.min.js: %w", err)
	}

	return &Renderer{
		chromePath: chromePath,
		workDir:    dir,
		jsPath:     jsPath,
		Theme:      "default",
		Timeout:    20 * time.Second,
	}, nil
}

// Close removes the renderer's temporary files.
func (r *Renderer) Close() error {
	return os.RemoveAll(r.workDir)
}

// ChromePath returns the detected or configured Chrome/Chromium path.
func (r *Renderer) ChromePath() string { return r.chromePath }

// pageTemplate renders a diagram through Mermaid.js and serializes the SVG in
// a plain-text script element so Chrome's DOM dump preserves its outerHTML.
const pageTemplate = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<style>
  html, body { margin:0; padding:0; background:#ffffff; }
  #stage { display:inline-block; padding:8px; background:#ffffff; }
</style>
</head>
<body>
<div id="stage"><pre class="mermaid" id="dgm">%s</pre></div>
<script src="mermaid.min.js"></script>
<script>
  function finish(errText) {
    var out = document.createElement("script");
    out.type = "text/plain";
    out.id = "svg-out";
    if (errText) {
      out.setAttribute("data-error", "1");
      out.textContent = errText;
    } else {
      var svgEl = document.querySelector("#dgm svg") || document.querySelector("svg");
      out.textContent = svgEl ? svgEl.outerHTML : "";
    }
    document.body.appendChild(out);
  }
  try {
    mermaid.initialize({
      startOnLoad: false,
      theme: %q,
      securityLevel: "loose",
      // Disable HTML labels because many Confluence instances sanitize SVG
      // attachments and remove <foreignObject>. Native SVG text keeps labels
      // visible without the associated HTML injection risk.
      flowchart: { htmlLabels: false },
      class: { htmlLabels: false },
      state: { htmlLabels: false },
      er: { htmlLabels: false }
    });
    mermaid.run({ querySelector: "#dgm" }).then(function () {
      finish(null);
    }).catch(function (e) {
      finish(e && e.message ? e.message : String(e));
    });
  } catch (e) {
    finish(e && e.message ? e.message : String(e));
  }
</script>
</body>
</html>`

var svgOutRE = regexp.MustCompile(`(?s)<script[^>]*\bid="svg-out"[^>]*>(.*?)</script>`)
var svgOutErrRE = regexp.MustCompile(`(?s)<script[^>]*\bid="svg-out"[^>]*\bdata-error="1"[^>]*>`)

// Render converts Mermaid source to SVG and writes it to destSVG.
func (r *Renderer) Render(source string, destSVG string) error {
	id := filepath.Base(destSVG)
	htmlPath := filepath.Join(r.workDir, id+".html")

	html := fmt.Sprintf(pageTemplate, escapeHTML(source), r.Theme)
	if err := os.WriteFile(htmlPath, []byte(html), 0o644); err != nil {
		return fmt.Errorf("write rendering page: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), r.Timeout)
	defer cancel()

	args := []string{
		"--headless=new",
		"--disable-gpu",
		"--no-sandbox",
		"--hide-scrollbars",
		"--disable-extensions",
		"--virtual-time-budget=4000",
		"--window-size=2400,1800",
		"--dump-dom",
		"file://" + filepath.ToSlash(htmlPath),
	}

	cmd := exec.CommandContext(ctx, r.chromePath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run headless Chrome (%s): %w\n%s", r.chromePath, err, stderr.String())
	}

	dom := stdout.String()
	m := svgOutRE.FindStringSubmatch(dom)
	if m == nil {
		return fmt.Errorf("Mermaid rendering did not produce an SVG before the browser stopped")
	}
	if svgOutErrRE.MatchString(dom) {
		return fmt.Errorf("invalid Mermaid diagram (check its syntax): %s", strings.TrimSpace(unescapeScriptText(m[1])))
	}

	svg := strings.TrimSpace(unescapeScriptText(m[1]))
	if svg == "" {
		return fmt.Errorf("empty or invalid Mermaid diagram (check its syntax)")
	}
	svg = fixSVGDimensions(svg)
	svg = addOpaqueBackground(svg, backgroundColorForTheme(r.Theme))
	if !strings.HasPrefix(svg, "<?xml") {
		svg = `<?xml version="1.0" encoding="UTF-8"?>` + "\n" + svg
	}

	if err := os.WriteFile(destSVG, []byte(svg), 0o644); err != nil {
		return fmt.Errorf("write SVG file: %w", err)
	}

	return nil
}

var svgOpenTagRE = regexp.MustCompile(`(?s)^<svg\b[^>]*>`)
var svgViewBoxRE = regexp.MustCompile(`viewBox="([-\d.eE]+)\s+([-\d.eE]+)\s+([\d.eE]+)\s+([\d.eE]+)"`)
var svgWidthAttrRE = regexp.MustCompile(`\s(width|height)="[^"]*"`)

// fixSVGDimensions replaces Mermaid's width="100%" root attribute with fixed
// pixel dimensions derived from the viewBox. Some consumers cannot otherwise
// infer an intrinsic image size.
func fixSVGDimensions(svg string) string {
	openTag := svgOpenTagRE.FindString(svg)
	if openTag == "" {
		return svg
	}
	vb := svgViewBoxRE.FindStringSubmatch(openTag)
	if vb == nil {
		return svg
	}
	// Remove existing dimensions before inserting fixed pixel values.
	withoutSize := svgWidthAttrRE.ReplaceAllString(openTag, "")
	newTag := "<svg width=\"" + vb[3] + "\" height=\"" + vb[4] + "\"" + strings.TrimPrefix(withoutSize, "<svg")
	return newTag + svg[len(openTag):]
}

// backgroundColorForTheme returns a background that keeps the diagram
// readable independently of the surrounding Confluence theme.
func backgroundColorForTheme(theme string) string {
	if theme == "dark" {
		return "#1e1e1e"
	}
	return "#ffffff"
}

// addOpaqueBackground inserts a viewBox-sized rectangle behind the diagram.
// Mermaid SVGs are transparent, so an explicit background prevents text and
// lines from becoming unreadable against the opposite Confluence theme.
func addOpaqueBackground(svg string, bg string) string {
	openTag := svgOpenTagRE.FindString(svg)
	if openTag == "" {
		return svg
	}
	vb := svgViewBoxRE.FindStringSubmatch(openTag)
	if vb == nil {
		return svg
	}
	rect := `<rect x="` + vb[1] + `" y="` + vb[2] + `" width="` + vb[3] + `" height="` + vb[4] + `" fill="` + bg + `"/>`
	return openTag + rect + svg[len(openTag):]
}

// unescapeScriptText reverses minimal escaping that some Chrome versions may
// apply while dumping the DOM.
func unescapeScriptText(s string) string {
	r := strings.NewReplacer(
		"&amp;", "&",
		"&lt;", "<",
		"&gt;", ">",
	)
	return r.Replace(s)
}

func escapeHTML(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
	)
	return r.Replace(s)
}

// findChrome locates an installed Chromium-based browser.
func findChrome() (string, error) {
	if p := os.Getenv("CONFLUENCE_PUBLISH_CHROME"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	var candidates []string
	switch runtime.GOOS {
	case "windows":
		candidates = []string{
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
			`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
			`C:\Program Files\Chromium\Application\chrome.exe`,
		}
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			candidates = append(candidates,
				filepath.Join(local, `Google\Chrome\Application\chrome.exe`),
				filepath.Join(local, `Microsoft\Edge\Application\msedge.exe`),
			)
		}
	case "darwin":
		candidates = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
		}
	default: // linux
		candidates = []string{
			"/usr/bin/google-chrome",
			"/usr/bin/google-chrome-stable",
			"/usr/bin/chromium",
			"/usr/bin/chromium-browser",
			"/snap/bin/chromium",
		}
	}

	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}

	for _, name := range []string{"google-chrome", "chromium", "chromium-browser", "msedge", "chrome"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}

	return "", fmt.Errorf("no Chrome/Chromium/Edge browser detected; install one or use --chrome-path")
}
