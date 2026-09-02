// Author: Emmanuel COLUSSI
// Copyright (c) 2026 Emmanuel COLUSSI
// SPDX-License-Identifier: MIT

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
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}
	default:
		// Convenience form: `confluence-publish file.md` without a subcommand.
		if err := cmdPublish(os.Args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}
	}
}

func printUsage() {
	fmt.Print(`confluence-publish - publish Markdown to Confluence Server/Data Center

Usage:
  confluence-publish config set --base-url URL --token PAT [--username U --password P] [--insecure] [--space KEY] [--parent REF] [--chrome-path PATH]
  confluence-publish config show

  confluence-publish publish <file.md> [options]
  confluence-publish <file.md> [options]             (shortcut for "publish")

"publish" options:
  --file PATH            Markdown file (or first positional argument)
  --dir PATH             publish every .md file in the directory
  --recursive            with --dir, include subdirectories
  --space KEY            Confluence space (overrides frontmatter/config)
  --parent REF           parent page title or ID (overrides frontmatter/config)
  --title "Title"        page title (overrides frontmatter/filename)
  --page-id ID           force an update of this exact page
  --labels a,b,c         additional labels (in addition to frontmatter)
  --base-url URL         override the configured Confluence URL
  --token PAT            override the configured token
  --username / --password  basic authentication (alternative to a token)
  --insecure             disable TLS verification (internal certificates)
  --chrome-path PATH     Chrome/Chromium/Edge path for Mermaid rendering
  --theme NAME           Mermaid theme: default|dark|neutral|forest (default: default)
  --no-mermaid           disable Mermaid image rendering
  --dry-run              print generated storage format without calling the API

Supported Markdown frontmatter (optional, at the beginning of the file):
  ---
  title: My page
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
		fmt.Println("Configuration file:", path)
		fmt.Println("base_url:      ", cfg.BaseURL)
		fmt.Println("token:         ", maskSecret(cfg.Token))
		fmt.Println("username:      ", cfg.Username)
		fmt.Println("password:      ", maskSecret(cfg.Password))
		fmt.Println("insecure:      ", cfg.Insecure)
		fmt.Println("chrome_path:   ", cfg.ChromePath)
		fmt.Println("default_space: ", cfg.Space)
		fmt.Println("default_parent:", cfg.Parent)
		fmt.Println("user_agent:    ", cfg.UserAgent)

	case "set":
		fs := goflag.NewFlagSet("config set", goflag.ExitOnError)
		baseURL := fs.String("base-url", "", "Confluence URL, e.g. https://confluence.example.com")
		token := fs.String("token", "", "Personal Access Token")
		username := fs.String("username", "", "username (basic authentication)")
		password := fs.String("password", "", "password (basic authentication)")
		insecure := fs.Bool("insecure", false, "disable TLS verification")
		chromePath := fs.String("chrome-path", "", "path to Chrome/Chromium/Edge")
		space := fs.String("space", "", "default Confluence space")
		parent := fs.String("parent", "", "default parent page (title or ID)")
		userAgent := fs.String("user-agent", "", "custom HTTP User-Agent")
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
		if set["user-agent"] {
			cfg.UserAgent = *userAgent
		}

		must(config.Save(cfg))
		path, _ := config.Path()
		fmt.Println("Configuration saved to", path)

	default:
		fmt.Fprintln(os.Stderr, "Usage: confluence-publish config [set|show] ...")
		os.Exit(1)
	}
}

func maskSecret(s string) string {
	if s == "" {
		return "(not set)"
	}
	if len(s) <= 4 {
		return "****"
	}
	return strings.Repeat("*", len(s)-4) + s[len(s)-4:]
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
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
	tree                         bool
}

// reorderArgs separates flags from positional arguments so both
// `confluence-publish file.md --dry-run` and
// `confluence-publish --dry-run file.md` work. Go's standard flag package
// otherwise stops parsing at the first positional argument.
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
	"tree":       true,
}

func cmdPublish(rawArgs []string) error {
	flagArgs, positional := reorderArgs(rawArgs, publishBoolFlags)

	fs := goflag.NewFlagSet("publish", goflag.ExitOnError)
	file := fs.String("file", "", "Markdown file")
	dir := fs.String("dir", "", "directory to publish (all .md files)")
	recursive := fs.Bool("recursive", false, "with --dir, include subdirectories (ignored with --tree)")
	tree := fs.Bool("tree", false, "with --dir, reproduce the directory tree as a Confluence page hierarchy (see README)")
	space := fs.String("space", "", "Confluence space")
	parent := fs.String("parent", "", "parent page (title or ID)")
	title := fs.String("title", "", "page title")
	pageID := fs.String("page-id", "", "ID of the page to update")
	labelsFlag := fs.String("labels", "", "comma-separated labels")
	baseURL := fs.String("base-url", "", "URL Confluence")
	token := fs.String("token", "", "Personal Access Token")
	username := fs.String("username", "", "username (basic authentication)")
	password := fs.String("password", "", "password (basic authentication)")
	insecure := fs.Bool("insecure", false, "disable TLS verification")
	chromePath := fs.String("chrome-path", "", "path to Chrome/Chromium/Edge")
	theme := fs.String("theme", "default", "Mermaid theme")
	noMermaid := fs.Bool("no-mermaid", false, "disable Mermaid image rendering")
	dryRun := fs.Bool("dry-run", false, "do not call the Confluence API")
	userAgent := fs.String("user-agent", "", "custom HTTP User-Agent")

	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	if *file == "" && *dir == "" && len(positional) > 0 {
		*file = positional[0]
	}
	if *file == "" && *dir == "" {
		return fmt.Errorf("specify a Markdown file (or --dir)")
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("read configuration: %w", err)
	}

	finalBaseURL := firstNonEmpty(*baseURL, cfg.BaseURL)
	finalToken := firstNonEmpty(*token, cfg.Token)
	finalUsername := firstNonEmpty(*username, cfg.Username)
	finalPassword := firstNonEmpty(*password, cfg.Password)
	finalInsecure := *insecure || cfg.Insecure
	finalChromePath := firstNonEmpty(*chromePath, cfg.ChromePath)
	finalUserAgent := firstNonEmpty(*userAgent, cfg.UserAgent)

	if !*dryRun {
		if finalBaseURL == "" {
			return fmt.Errorf("missing Confluence URL: use --base-url or `confluence-publish config set --base-url ...`")
		}
		if finalToken == "" && finalUsername == "" {
			return fmt.Errorf("missing authentication: use --token (or --username/--password), or `confluence-publish config set --token ...`")
		}
	}

	opts := &publishOptions{
		space:  firstNonEmpty(*space, cfg.Space),
		parent: firstNonEmpty(*parent, cfg.Parent),
		title:  *title,
		pageID: *pageID,
		theme:  *theme,
		dryRun: *dryRun,
		tree:   *tree,
	}
	if *labelsFlag != "" {
		opts.labels = splitAndTrim(*labelsFlag, ",")
	}

	if !*dryRun {
		opts.client = confluence.New(finalBaseURL, finalToken, finalUsername, finalPassword, finalInsecure)
		if finalUserAgent != "" {
			opts.client.UserAgent = finalUserAgent
		}
	}

	if !*noMermaid {
		r, err := mermaid.NewRenderer(finalChromePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Mermaid rendering disabled (%v). Diagrams will be inserted as code blocks. Use --chrome-path to specify your browser.\n", err)
		} else {
			opts.mermaidRenderer = r
			defer r.Close()
		}
	}

	if *dir != "" {
		if opts.tree {
			return publishDirTree(*dir, opts)
		}
		return publishDir(*dir, *recursive, opts)
	}
	_, err = publishFile(*file, opts)
	return err
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
		return fmt.Errorf("read directory %s: %w", dir, err)
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
		fmt.Fprintln(os.Stderr, "No .md files found in", dir)
	}
	for _, f := range files {
		// Directory mode cannot force one global title. Use each file's
		// frontmatter or filename instead.
		perFile := *opts
		perFile.title = ""
		perFile.pageID = ""
		if _, err := publishFile(f, &perFile); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to publish %s: %v\n", f, err)
		}
	}
	return nil
}

// findIndexFile returns a directory's explicit index file (_index.md or
// index.md), or an empty string when neither exists.
func findIndexFile(dir string) string {
	for _, name := range []string{"_index.md", "index.md"} {
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}

// mdFilesIn lists .md files directly inside dir, excluding subdirectories.
// It returns nil when dir cannot be read.
func mdFilesIn(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.EqualFold(filepath.Ext(e.Name()), ".md") {
			out = append(out, e.Name())
		}
	}
	return out
}

// publishDirTree publishes dir as a tree. The contents of dir become children
// of opts.parent; dir itself does not create a page. Subdirectories are
// represented by an explicit index page or an automatically created container.
func publishDirTree(dir string, opts *publishOptions) error {
	spaceKey := opts.space
	if !opts.dryRun && spaceKey == "" {
		return fmt.Errorf("missing Confluence space (--space or default_space configuration)")
	}

	parentID := opts.parent
	if !opts.dryRun && parentID != "" {
		var err error
		parentID, err = opts.client.ResolveParentID(spaceKey, opts.parent)
		if err != nil {
			return err
		}
	}

	if idx := findIndexFile(dir); idx != "" {
		fmt.Fprintf(os.Stderr, "Note: %s is ignored (the directory passed to --dir does not create its own page; only its contents are published under --parent).\n", idx)
	}

	return publishTree(dir, parentID, opts)
}

func publishTree(dir string, parentID string, opts *publishOptions) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read directory %s: %w", dir, err)
	}
	ownIndex := findIndexFile(dir)

	for _, e := range entries {
		full := filepath.Join(dir, e.Name())

		if e.IsDir() {
			subIndex := findIndexFile(full)
			mdInSub := mdFilesIn(full)

			if subIndex == "" && len(mdInSub) == 0 {
				// A directory without an index or direct .md file remains transparent;
				// deeper content is attached to the current parent.
				if err := publishTree(full, parentID, opts); err != nil {
					return err
				}
				continue
			}

			var page *confluence.Page
			var err error

			if subIndex != "" {
				// An explicit _index.md/index.md supplies the directory page content.
				perFile := *opts
				perFile.parent = parentID
				perFile.title = ""
				perFile.pageID = ""
				page, err = publishFile(subIndex, &perFile)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Failed to publish %s: %v\n", subIndex, err)
					continue
				}
			} else {
				// Direct Markdown without an explicit index creates a container page
				// whose title is derived from the directory name.
				page, err = publishFolderPage(full, parentID, opts)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Failed to publish directory %s: %v\n", full, err)
					continue
				}
			}

			childParentID := parentID
			if opts.dryRun {
				// Use a readable placeholder because dry-run does not create a page.
				childParentID = "<page to create: " + filepath.Base(full) + ">"
			}
			if page != nil {
				childParentID = page.ID
			}
			if err := publishTree(full, childParentID, opts); err != nil {
				return err
			}
			continue
		}

		if !strings.EqualFold(filepath.Ext(e.Name()), ".md") {
			continue
		}
		if ownIndex != "" && full == ownIndex {
			continue // already published by the caller as this directory's page
		}

		perFile := *opts
		perFile.parent = parentID
		perFile.title = ""
		perFile.pageID = ""
		if _, err := publishFile(full, &perFile); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to publish %s: %v\n", full, err)
		}
	}
	return nil
}

// publishFolderPage creates or updates the container page for a directory
// without an explicit index but with direct Markdown content. Its title is
// derived from the directory name, and the Confluence children macro supplies
// its body automatically.
func publishFolderPage(dir string, parentID string, opts *publishOptions) (*confluence.Page, error) {
	title := titleFromFilename(dir)
	storage := `<p><ac:structured-macro ac:name="children" ac:schema-version="2" /></p>`

	if opts.dryRun {
		parentInfo := parentID
		if parentInfo == "" {
			parentInfo = "(space root)"
		}
		fmt.Printf("=== [directory] %s (%s) [parent: %s] ===\n", dir, title, parentInfo)
		fmt.Println(storage)
		return nil, nil
	}

	spaceKey := opts.space
	if spaceKey == "" {
		return nil, fmt.Errorf("missing Confluence space (--space or default_space configuration)")
	}
	client := opts.client

	page, err := client.FindPageByTitle(spaceKey, title)
	if err != nil {
		return nil, fmt.Errorf("find page %q in %s: %w", title, spaceKey, err)
	}

	if page == nil {
		fmt.Printf("Creating container page %q in space %s...\n", title, spaceKey)
		page, err = client.CreatePage(spaceKey, title, parentID, storage)
		if err != nil {
			return nil, fmt.Errorf("create container page: %w", err)
		}
	} else if page.Title == title && normalizeStorage(page.StorageValue()) == normalizeStorage(storage) {
		fmt.Printf("Container page %q (ID %s) is already up to date; no changes.\n", page.Title, page.ID)
	} else {
		fmt.Printf("Updating container page %q (ID %s, version %d)...\n", page.Title, page.ID, page.Version.Number)
		page, err = client.UpdatePage(page.ID, title, storage, page.Version.Number)
		if err != nil {
			return nil, fmt.Errorf("update container page: %w", err)
		}
	}

	fmt.Println("OK ->", client.URL(page))
	return page, nil
}

func publishFile(path string, opts *publishOptions) (*confluence.Page, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	meta, body, err := frontmatter.Split(data)
	if err != nil {
		return nil, fmt.Errorf("invalid frontmatter in %s: %w", path, err)
	}

	title := firstNonEmpty(opts.title, meta.Title, titleFromFilename(path))
	spaceKey := firstNonEmpty(opts.space, meta.Space)
	parentRef := firstNonEmpty(opts.parent, meta.Parent)
	pageID := firstNonEmpty(opts.pageID, meta.PageID)
	labels := append(append([]string{}, meta.Labels...), opts.labels...)

	if !opts.dryRun && spaceKey == "" && pageID == "" {
		return nil, fmt.Errorf("missing Confluence space for %s (--space, 'space:' frontmatter, or default_space configuration)", path)
	}

	mermaidOutDir, err := os.MkdirTemp("", "confluence-publish-out-*")
	if err != nil {
		return nil, err
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
		return nil, fmt.Errorf("convert Markdown %s: %w", path, err)
	}
	for _, w := range result.Warnings {
		fmt.Fprintf(os.Stderr, "[%s] Warning: %s\n", filepath.Base(path), w)
	}

	if opts.dryRun {
		parentInfo := parentRef
		if parentInfo == "" {
			parentInfo = "(space root)"
		}
		fmt.Printf("=== %s (%s) [parent: %s] ===\n", path, title, parentInfo)
		fmt.Println(result.Storage)
		if len(result.Attachments) > 0 {
			fmt.Println("--- Attachments ---")
			for _, a := range result.Attachments {
				fmt.Println(" -", a.Filename, "<-", a.LocalPath)
			}
		}
		return nil, nil
	}

	client := opts.client

	var parentID string
	if parentRef != "" {
		parentID, err = client.ResolveParentID(spaceKey, parentRef)
		if err != nil {
			return nil, err
		}
	}

	var page *confluence.Page
	if pageID != "" {
		page, err = client.GetPage(pageID)
		if err != nil {
			return nil, fmt.Errorf("get page ID %s: %w", pageID, err)
		}
	} else {
		page, err = client.FindPageByTitle(spaceKey, title)
		if err != nil {
			return nil, fmt.Errorf("find page %q in %s: %w", title, spaceKey, err)
		}
	}

	if page == nil {
		fmt.Printf("Creating page %q in space %s...\n", title, spaceKey)
		page, err = client.CreatePage(spaceKey, title, parentID, result.Storage)
		if err != nil {
			return nil, fmt.Errorf("create page: %w", err)
		}
	} else if page.Title == title && normalizeStorage(page.StorageValue()) == normalizeStorage(result.Storage) {
		fmt.Printf("Page %q (ID %s) is already up to date; no changes.\n", page.Title, page.ID)
	} else {
		fmt.Printf("Updating page %q (ID %s, version %d)...\n", page.Title, page.ID, page.Version.Number)
		page, err = client.UpdatePage(page.ID, title, result.Storage, page.Version.Number)
		if err != nil {
			return nil, fmt.Errorf("update page: %w", err)
		}
	}

	for _, a := range result.Attachments {
		skipped, err := client.UploadAttachment(page.ID, a.Filename, a.LocalPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  Warning: failed to upload %s: %v\n", a.Filename, err)
			continue
		}
		if skipped {
			fmt.Printf("  attachment: %s (already up to date, skipped)\n", a.Filename)
		} else {
			fmt.Printf("  attachment: %s\n", a.Filename)
		}
	}

	if len(labels) > 0 {
		if err := client.AddLabels(page.ID, labels); err != nil {
			fmt.Fprintf(os.Stderr, "  Warning: failed to add labels: %v\n", err)
		}
	}

	fmt.Println("OK ->", client.URL(page))
	return page, nil
}

// normalizeStorage flattens whitespace differences between two XHTML storage
// values. It is not a semantic comparison, but reliably detects common
// no-change cases despite cosmetic reformatting by Confluence.
func normalizeStorage(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func titleFromFilename(path string) string {
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	base = strings.ReplaceAll(base, "_", " ")
	base = strings.ReplaceAll(base, "-", " ")
	words := strings.Fields(base)
	for i, w := range words {
		if w == strings.ToUpper(w) {
			continue // Preserve all-uppercase abbreviations such as API and README.
		}
		r := []rune(w)
		if len(r) > 0 {
			r[0] = []rune(strings.ToUpper(string(r[0])))[0]
			words[i] = string(r)
		}
	}
	return strings.Join(words, " ")
}
