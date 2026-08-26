// Package mermaid rend des diagrammes Mermaid en image PNG en pilotant un
// Chrome/Chromium local en mode headless (screenshot en ligne de commande).
//
// Aucune dépendance externe n'est requise à l'exécution à part un
// navigateur Chromium-based déjà installé sur la machine (Chrome, Edge ou
// Chromium). Le rendu est 100% local/offline : mermaid.js est embarqué
// dans le binaire.
package mermaid

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

//go:embed assets/mermaid.min.js
var mermaidJS []byte

// Renderer rend des sources Mermaid en fichiers PNG.
type Renderer struct {
	chromePath string
	workDir    string
	jsPath     string
	Theme      string        // "default", "dark", "neutral", "forest"...
	Timeout    time.Duration // timeout par diagramme
}

// NewRenderer prépare un Renderer. chromePath peut être vide pour une
// détection automatique du navigateur installé.
func NewRenderer(chromePath string) (*Renderer, error) {
	if chromePath == "" {
		var err error
		chromePath, err = findChrome()
		if err != nil {
			return nil, err
		}
	} else if _, err := os.Stat(chromePath); err != nil {
		return nil, fmt.Errorf("chemin Chrome invalide %q: %w", chromePath, err)
	}

	dir, err := os.MkdirTemp("", "confluence-publish-mermaid-*")
	if err != nil {
		return nil, fmt.Errorf("création répertoire temporaire: %w", err)
	}

	jsPath := filepath.Join(dir, "mermaid.min.js")
	if err := os.WriteFile(jsPath, mermaidJS, 0o644); err != nil {
		return nil, fmt.Errorf("écriture mermaid.min.js: %w", err)
	}

	return &Renderer{
		chromePath: chromePath,
		workDir:    dir,
		jsPath:     jsPath,
		Theme:      "default",
		Timeout:    20 * time.Second,
	}, nil
}

// Close nettoie les fichiers temporaires du renderer.
func (r *Renderer) Close() error {
	return os.RemoveAll(r.workDir)
}

// ChromePath retourne le chemin du binaire Chrome/Chromium détecté ou fourni.
func (r *Renderer) ChromePath() string { return r.chromePath }

const pageTemplate = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<style>
  html, body { margin:0; padding:0; background:#ffffff; }
  #stage { display:inline-block; padding:24px; background:#ffffff; }
  #err { color:#c0392b; font:14px monospace; white-space:pre-wrap; padding:16px; border:2px solid #c0392b; display:none; max-width:1800px; }
</style>
</head>
<body>
<div id="stage"><pre class="mermaid" id="dgm">%s</pre></div>
<div id="err"></div>
<script src="mermaid.min.js"></script>
<script>
  window.__renderDone = false;
  try {
    mermaid.initialize({ startOnLoad: false, theme: %q, securityLevel: "loose" });
    mermaid.run({ querySelector: "#dgm" }).then(function () {
      window.__renderDone = true;
    }).catch(function (e) {
      document.getElementById("stage").style.display = "none";
      var el = document.getElementById("err");
      el.style.display = "block";
      el.textContent = "Erreur de rendu Mermaid:\n" + (e && e.message ? e.message : String(e));
      window.__renderDone = true;
    });
  } catch (e) {
    document.getElementById("stage").style.display = "none";
    var el = document.getElementById("err");
    el.style.display = "block";
    el.textContent = "Erreur de rendu Mermaid:\n" + (e && e.message ? e.message : String(e));
    window.__renderDone = true;
  }
</script>
</body>
</html>`

// Render rend le code source mermaid `source` et retourne le chemin d'un
// fichier PNG recadré (fond blanc retiré, marge de sécurité conservée).
// destPNG est le chemin de sortie souhaité.
func (r *Renderer) Render(source string, destPNG string) error {
	id := filepath.Base(destPNG)
	htmlPath := filepath.Join(r.workDir, id+".html")
	rawPNG := filepath.Join(r.workDir, id+".raw.png")

	html := fmt.Sprintf(pageTemplate, escapeHTML(source), r.Theme)
	if err := os.WriteFile(htmlPath, []byte(html), 0o644); err != nil {
		return fmt.Errorf("écriture page de rendu: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), r.Timeout)
	defer cancel()

	args := []string{
		"--headless=new",
		"--disable-gpu",
		"--no-sandbox",
		"--hide-scrollbars",
		"--force-color-profile=srgb",
		"--disable-extensions",
		"--default-background-color=FFFFFFFF",
		"--virtual-time-budget=4000",
		"--window-size=2400,1800",
		"--screenshot=" + rawPNG,
		"file://" + filepath.ToSlash(htmlPath),
	}

	cmd := exec.CommandContext(ctx, r.chromePath, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("échec exécution Chrome headless (%s): %w\n%s", r.chromePath, err, stderr.String())
	}

	f, err := os.Open(rawPNG)
	if err != nil {
		return fmt.Errorf("capture d'écran introuvable, le rendu a probablement échoué: %w", err)
	}
	defer f.Close()

	img, err := png.Decode(f)
	if err != nil {
		return fmt.Errorf("décodage PNG: %w", err)
	}

	cropped, err := autocrop(img, color.White, 8, 16)
	if err != nil {
		return fmt.Errorf("diagramme mermaid vide ou invalide (vérifiez la syntaxe): %w", err)
	}

	out, err := os.Create(destPNG)
	if err != nil {
		return fmt.Errorf("création fichier de sortie: %w", err)
	}
	defer out.Close()

	if err := png.Encode(out, cropped); err != nil {
		return fmt.Errorf("encodage PNG final: %w", err)
	}

	return nil
}

// autocrop recadre img à la zone contenant des pixels différents de bg
// (au-delà de tolerance par canal), avec padding pixels de marge conservée.
func autocrop(img image.Image, bg color.Color, tolerance int, padding int) (image.Image, error) {
	bounds := img.Bounds()
	br, bgc, bb, _ := bg.RGBA()

	differs := func(x, y int) bool {
		r, g, b, _ := img.At(x, y).RGBA()
		return absInt(int(r>>8)-int(br>>8)) > tolerance ||
			absInt(int(g>>8)-int(bgc>>8)) > tolerance ||
			absInt(int(b>>8)-int(bb>>8)) > tolerance
	}

	minX, minY := bounds.Max.X, bounds.Max.Y
	maxX, maxY := bounds.Min.X, bounds.Min.Y
	found := false

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if differs(x, y) {
				found = true
				if x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
				if y < minY {
					minY = y
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}

	if !found {
		return nil, fmt.Errorf("aucun contenu détecté dans la capture")
	}

	minX = maxInt(bounds.Min.X, minX-padding)
	minY = maxInt(bounds.Min.Y, minY-padding)
	maxX = minInt(bounds.Max.X-1, maxX+padding)
	maxY = minInt(bounds.Max.Y-1, maxY+padding)

	rect := image.Rect(0, 0, maxX-minX+1, maxY-minY+1)
	dst := image.NewRGBA(rect)
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			dst.Set(x-minX, y-minY, img.At(x, y))
		}
	}
	return dst, nil
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func escapeHTML(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
	)
	return r.Replace(s)
}

// findChrome cherche un navigateur Chromium-based installé sur la machine.
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

	return "", fmt.Errorf("aucun navigateur Chrome/Chromium/Edge détecté ; installez Chrome ou passez --chrome-path")
}
