// Command confluence-publish publie un (ou plusieurs) fichier(s) Markdown
// comme page(s) Confluence via l'API REST, en gérant les images locales et
// les diagrammes Mermaid (rendus en PNG et uploadés en pièce jointe).
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"confluence-publish/internal/config"
	"confluence-publish/internal/confluence"
	"confluence-publish/internal/frontmatter"
	"confluence-publish/internal/mdconvert"
	"confluence-publish/internal/mermaid"

	goflag "flag"
)

const version = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "help", "-h", "--help":
		printUsage()
	case "version", "-v", "--version":
		fmt.Println("confluence-publish " + version)
	case "config":
		cmdConfig(os.Args[2:])
	case "publish":
		if err := cmdPublish(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "Erreur:", err)
			os.Exit(1)
		}
	default:
		// Confort : `confluence-publish fichier.md` sans sous-commande.
		if err := cmdPublish(os.Args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "Erreur:", err)
			os.Exit(1)
		}
	}
}

func printUsage() {
	fmt.Print(`confluence-publish - publie du Markdown vers Confluence Server/Data Center

Usage:
  confluence-publish config set --base-url URL --token PAT [--username U --password P] [--insecure] [--space KEY] [--parent REF] [--chrome-path PATH]
  confluence-publish config show

  confluence-publish publish <fichier.md> [options]
  confluence-publish <fichier.md> [options]          (raccourci de "publish")

Options de "publish":
  --file PATH            fichier markdown (ou 1er argument positionnel)
  --dir PATH              publie tous les .md du répertoire
  --recursive             avec --dir, parcourt aussi les sous-répertoires
  --space KEY             espace Confluence (remplace le frontmatter/config)
  --parent REF             page parente : titre ou id (remplace le frontmatter/config)
  --title "Titre"          titre de la page (remplace le frontmatter/nom de fichier)
  --page-id ID             force la mise à jour de cette page précise
  --labels a,b,c           labels ajoutés (en plus du frontmatter)
  --base-url URL           remplace l'URL Confluence de la config
  --token PAT              remplace le token de la config
  --username / --password  authentification basique (alternative au token)
  --insecure               ignore la vérification TLS (certificats internes)
  --chrome-path PATH       chemin vers Chrome/Chromium/Edge pour le rendu Mermaid
  --theme NAME             thème mermaid: default|dark|neutral|forest (défaut: default)
  --no-mermaid             désactive le rendu image des diagrammes mermaid
  --dry-run                affiche le storage format généré sans appeler l'API

Frontmatter markdown reconnu (optionnel, en tête de fichier):
  ---
  title: Ma page
  space: DEV
  parent: 123456
  page_id: 987654
  labels: [architecture, backend]
  ---
`)
}

// ---- config ----

func cmdConfig(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: confluence-publish config [set|show] ...")
		os.Exit(1)
	}
	switch args[0] {
	case "show":
		cfg, err := config.Load()
		must(err)
		path, _ := config.Path()
		fmt.Println("Fichier de config:", path)
		fmt.Println("base_url:      ", cfg.BaseURL)
		fmt.Println("token:         ", maskSecret(cfg.Token))
		fmt.Println("username:      ", cfg.Username)
		fmt.Println("password:      ", maskSecret(cfg.Password))
		fmt.Println("insecure:      ", cfg.Insecure)
		fmt.Println("chrome_path:   ", cfg.ChromePath)
		fmt.Println("default_space: ", cfg.Space)
		fmt.Println("default_parent:", cfg.Parent)

	case "set":
		fs := goflag.NewFlagSet("config set", goflag.ExitOnError)
		baseURL := fs.String("base-url", "", "URL Confluence, ex: https://confluence.exemple.com")
		token := fs.String("token", "", "Personal Access Token")
		username := fs.String("username", "", "utilisateur (auth basique)")
		password := fs.String("password", "", "mot de passe (auth basique)")
		insecure := fs.Bool("insecure", false, "ignore la vérification TLS")
		chromePath := fs.String("chrome-path", "", "chemin vers Chrome/Chromium/Edge")
		space := fs.String("space", "", "espace Confluence par défaut")
		parent := fs.String("parent", "", "page parente par défaut (titre ou id)")
		_ = fs.Parse(args[1:])

		cfg, err := config.Load()
		must(err)

		set := map[string]bool{}
		fs.Visit(func(f *goflag.Flag) { set[f.Name] = true })

		if set["base-url"] {
			cfg.BaseURL = *baseURL
		}
		if set["token"] {
			cfg.Token = *token
		}
		if set["username"] {
			cfg.Username = *username
		}
		if set["password"] {
			cfg.Password = *password
		}
		if set["insecure"] {
			cfg.Insecure = *insecure
		}
		if set["chrome-path"] {
			cfg.ChromePath = *chromePath
		}
		if set["space"] {
			cfg.Space = *space
		}
		if set["parent"] {
			cfg.Parent = *parent
		}

		must(config.Save(cfg))
		path, _ := config.Path()
		fmt.Println("Configuration sauvegardée dans", path)

	default:
		fmt.Fprintln(os.Stderr, "Usage: confluence-publish config [set|show] ...")
		os.Exit(1)
	}
}

func maskSecret(s string) string {
	if s == "" {
		return "(non défini)"
	}
	if len(s) <= 4 {
		return "****"
	}
	return strings.Repeat("*", len(s)-4) + s[len(s)-4:]
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "Erreur:", err)
		os.Exit(1)
	}
}

// ---- publish ----

type publishOptions struct {
	space, parent, title, pageID string
	labels                       []string
	client                       *confluence.Client
	mermaidRenderer              *mermaid.Renderer
	theme                        string
	dryRun                       bool
}

// reorderArgs sépare les arguments en (flags, positionnels) pour permettre
// d'écrire indifféremment `confluence-publish fichier.md --dry-run` ou
// `confluence-publish --dry-run fichier.md` : le package flag standard de
// Go arrête sinon l'analyse des flags dès le premier argument positionnel.
func reorderArgs(args []string, boolFlags map[string]bool) (flagArgs, positional []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flagArgs = append(flagArgs, a)
			name := strings.TrimLeft(a, "-")
			if strings.Contains(name, "=") {
				continue
			}
			if !boolFlags[name] && i+1 < len(args) {
				flagArgs = append(flagArgs, args[i+1])
				i++
			}
			continue
		}
		positional = append(positional, a)
	}
	return
}

var publishBoolFlags = map[string]bool{
	"recursive":  true,
	"insecure":   true,
	"no-mermaid": true,
	"dry-run":    true,
}

func cmdPublish(rawArgs []string) error {
	flagArgs, positional := reorderArgs(rawArgs, publishBoolFlags)

	fs := goflag.NewFlagSet("publish", goflag.ExitOnError)
	file := fs.String("file", "", "fichier markdown")
	dir := fs.String("dir", "", "répertoire à publier (tous les .md)")
	recursive := fs.Bool("recursive", false, "avec --dir, inclut les sous-répertoires")
	space := fs.String("space", "", "espace Confluence")
	parent := fs.String("parent", "", "page parente (titre ou id)")
	title := fs.String("title", "", "titre de la page")
	pageID := fs.String("page-id", "", "id de page à mettre à jour")
	labelsFlag := fs.String("labels", "", "labels séparés par des virgules")
	baseURL := fs.String("base-url", "", "URL Confluence")
	token := fs.String("token", "", "Personal Access Token")
	username := fs.String("username", "", "utilisateur (auth basique)")
	password := fs.String("password", "", "mot de passe (auth basique)")
	insecure := fs.Bool("insecure", false, "ignore la vérification TLS")
	chromePath := fs.String("chrome-path", "", "chemin vers Chrome/Chromium/Edge")
	theme := fs.String("theme", "default", "thème mermaid")
	noMermaid := fs.Bool("no-mermaid", false, "désactive le rendu image mermaid")
	dryRun := fs.Bool("dry-run", false, "n'appelle pas l'API Confluence")

	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	if *file == "" && *dir == "" && len(positional) > 0 {
		*file = positional[0]
	}
	if *file == "" && *dir == "" {
		return fmt.Errorf("indiquez un fichier markdown (ou --dir)")
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("lecture config: %w", err)
	}

	finalBaseURL := firstNonEmpty(*baseURL, cfg.BaseURL)
	finalToken := firstNonEmpty(*token, cfg.Token)
	finalUsername := firstNonEmpty(*username, cfg.Username)
	finalPassword := firstNonEmpty(*password, cfg.Password)
	finalInsecure := *insecure || cfg.Insecure
	finalChromePath := firstNonEmpty(*chromePath, cfg.ChromePath)

	if !*dryRun {
		if finalBaseURL == "" {
			return fmt.Errorf("URL Confluence manquante : utilisez --base-url ou `confluence-publish config set --base-url ...`")
		}
		if finalToken == "" && finalUsername == "" {
			return fmt.Errorf("authentification manquante : utilisez --token (ou --username/--password), ou `confluence-publish config set --token ...`")
		}
	}

	opts := &publishOptions{
		space:  firstNonEmpty(*space, cfg.Space),
		parent: firstNonEmpty(*parent, cfg.Parent),
		title:  *title,
		pageID: *pageID,
		theme:  *theme,
		dryRun: *dryRun,
	}
	if *labelsFlag != "" {
		opts.labels = splitAndTrim(*labelsFlag, ",")
	}

	if !*dryRun {
		opts.client = confluence.New(finalBaseURL, finalToken, finalUsername, finalPassword, finalInsecure)
	}

	if !*noMermaid {
		r, err := mermaid.NewRenderer(finalChromePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Attention: rendu Mermaid désactivé (%v). Les diagrammes seront insérés en bloc de code. Utilisez --chrome-path pour indiquer votre navigateur.\n", err)
		} else {
			opts.mermaidRenderer = r
			defer r.Close()
		}
	}

	if *dir != "" {
		return publishDir(*dir, *recursive, opts)
	}
	return publishFile(*file, opts)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func splitAndTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func publishDir(dir string, recursive bool, opts *publishOptions) error {
	var files []string
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("lecture répertoire %s: %w", dir, err)
	}
	for _, e := range entries {
		full := filepath.Join(dir, e.Name())
		if e.IsDir() {
			if recursive {
				if err := publishDir(full, recursive, opts); err != nil {
					return err
				}
			}
			continue
		}
		if strings.EqualFold(filepath.Ext(e.Name()), ".md") {
			files = append(files, full)
		}
	}
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "Aucun fichier .md trouvé dans", dir)
	}
	for _, f := range files {
		// En mode répertoire, le titre ne peut pas être forcé globalement :
		// on repart du frontmatter / nom de fichier pour chaque page.
		perFile := *opts
		perFile.title = ""
		perFile.pageID = ""
		if err := publishFile(f, &perFile); err != nil {
			fmt.Fprintf(os.Stderr, "Échec pour %s: %v\n", f, err)
		}
	}
	return nil
}

func publishFile(path string, opts *publishOptions) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("lecture %s: %w", path, err)
	}

	meta, body, err := frontmatter.Split(data)
	if err != nil {
		return fmt.Errorf("frontmatter invalide dans %s: %w", path, err)
	}

	title := firstNonEmpty(opts.title, meta.Title, titleFromFilename(path))
	spaceKey := firstNonEmpty(opts.space, meta.Space)
	parentRef := firstNonEmpty(opts.parent, meta.Parent)
	pageID := firstNonEmpty(opts.pageID, meta.PageID)
	labels := append(append([]string{}, meta.Labels...), opts.labels...)

	if !opts.dryRun && spaceKey == "" && pageID == "" {
		return fmt.Errorf("espace Confluence manquant pour %s (--space, frontmatter 'space:', ou config default_space)", path)
	}

	mermaidOutDir, err := os.MkdirTemp("", "confluence-publish-out-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(mermaidOutDir)

	if opts.mermaidRenderer != nil {
		opts.mermaidRenderer.Theme = opts.theme
	}

	converter := &mdconvert.Converter{
		BaseDir:       filepath.Dir(path),
		MermaidOutDir: mermaidOutDir,
	}
	if opts.mermaidRenderer != nil {
		converter.Mermaid = opts.mermaidRenderer
	}

	result, err := converter.Convert(body)
	if err != nil {
		return fmt.Errorf("conversion markdown %s: %w", path, err)
	}
	for _, w := range result.Warnings {
		fmt.Fprintf(os.Stderr, "[%s] Attention: %s\n", filepath.Base(path), w)
	}

	if opts.dryRun {
		fmt.Println("=== " + path + " (" + title + ") ===")
		fmt.Println(result.Storage)
		if len(result.Attachments) > 0 {
			fmt.Println("--- Pièces jointes ---")
			for _, a := range result.Attachments {
				fmt.Println(" -", a.Filename, "<-", a.LocalPath)
			}
		}
		return nil
	}

	client := opts.client

	var parentID string
	if parentRef != "" {
		parentID, err = client.ResolveParentID(spaceKey, parentRef)
		if err != nil {
			return err
		}
	}

	var page *confluence.Page
	if pageID != "" {
		page, err = client.GetPage(pageID)
		if err != nil {
			return fmt.Errorf("récupération page id %s: %w", pageID, err)
		}
	} else {
		page, err = client.FindPageByTitle(spaceKey, title)
		if err != nil {
			return fmt.Errorf("recherche page %q dans %s: %w", title, spaceKey, err)
		}
	}

	if page == nil {
		fmt.Printf("Création de la page %q dans l'espace %s...\n", title, spaceKey)
		page, err = client.CreatePage(spaceKey, title, parentID, result.Storage)
		if err != nil {
			return fmt.Errorf("création page: %w", err)
		}
	} else {
		fmt.Printf("Mise à jour de la page %q (id %s, version %d)...\n", page.Title, page.ID, page.Version.Number)
		page, err = client.UpdatePage(page.ID, title, result.Storage, page.Version.Number)
		if err != nil {
			return fmt.Errorf("mise à jour page: %w", err)
		}
	}

	for _, a := range result.Attachments {
		fmt.Printf("  pièce jointe: %s\n", a.Filename)
		if err := client.UploadAttachment(page.ID, a.Filename, a.LocalPath); err != nil {
			fmt.Fprintf(os.Stderr, "  Attention: échec upload %s: %v\n", a.Filename, err)
		}
	}

	if len(labels) > 0 {
		if err := client.AddLabels(page.ID, labels); err != nil {
			fmt.Fprintf(os.Stderr, "  Attention: échec ajout labels: %v\n", err)
		}
	}

	fmt.Println("OK ->", client.URL(page))
	return nil
}

func titleFromFilename(path string) string {
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	base = strings.ReplaceAll(base, "_", " ")
	base = strings.ReplaceAll(base, "-", " ")
	words := strings.Fields(base)
	for i, w := range words {
		if w == strings.ToUpper(w) {
			continue // sigles : on laisse tel quel (API, README, ...)
		}
		r := []rune(w)
		if len(r) > 0 {
			r[0] = []rune(strings.ToUpper(string(r[0])))[0]
			words[i] = string(r)
		}
	}
	return strings.Join(words, " ")
}
