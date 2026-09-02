# ScribeFlow : Markdown Publisher for Confluence

ScribeFlow is a command-line tool that publishes one or more Markdown files as
Confluence pages through the REST API. It also uploads referenced local images
and rendered Mermaid diagrams as Confluence attachments.

The application is distributed as a single Go binary for Windows and macOS.

Current release: **v1.0.0**.

What it solves:

- Markdown is rendered correctly in Confluence (headings, lists, tables, code, quotations, task lists, and more), rather than approximated by copying and pasting from a Markdown editor.
- **Mermaid** diagrams (` ```mermaid `) are rendered locally as **vector SVG** files (through headless Chrome/Edge, fully offline) and uploaded as attachments. They therefore display correctly even when Confluence has no Mermaid plugin and remain sharp at any zoom level. The SVG includes a solid background (white, or dark with `--theme dark`) so that it remains readable with either a light or dark Confluence theme. Labels use native SVG text instead of embedded HTML (`<foreignObject>`), which many Confluence instances remove for SVG attachment security.
- **Local images** referenced in Markdown (`![alt](./img.png)`) are uploaded automatically as attachments and linked from the page.
- Running the tool again on the same file **updates** the existing page and its attachments instead of creating another page.
- If the generated content and title already match Confluence, the update is skipped. Attachments whose byte size has not changed are skipped as well. This is a practical normalized-text/file-size heuristic rather than a perfect diff, but it covers the common no-change rerun case.

Confluence requirement: **Server / Data Center** (REST API v1). Chrome, Chromium, or Edge must be installed locally to render Mermaid diagrams. Use `--no-mermaid` when no supported browser is available.

## Installation

Copy the binary for your platform from `dist/` to a location of your choice, such as a directory in your `PATH`:

- Windows: `confluence-publish-windows-amd64.exe`
- macOS (Apple Silicon): `confluence-publish-darwin-arm64`
- macOS (Intel): `confluence-publish-darwin-amd64`

On macOS, Gatekeeper may require approval on first launch (right-click and select Open, or run `xattr -d com.apple.quarantine <binary>`).

To build from source (Go 1.21 or newer is required):

```bash
./scripts/fetch-mermaid.sh   # download mermaid.min.js once
./scripts/build.sh           # generate all three binaries in dist/
```

## Configuration

First create a **Personal Access Token** in Confluence (Profile menu → Account settings → Personal Access Tokens on Server/Data Center), then run:

```bash
confluence-publish config set \
  --base-url https://confluence.example.com \
  --token YOUR_PAT \
  --space DEV
```

`--space` sets the default Confluence space and can be overridden per file. Other useful options:

- `--insecure`: disables TLS certificate verification for an internal self-signed instance. Avoid this in production.
- `--username` / `--password`: basic authentication for instances that do not accept PATs.
- `--chrome-path`: explicit path to Chrome, Chromium, or Edge when automatic detection fails.
- `--parent`: default parent page title or ID.
- `--user-agent`: custom HTTP User-Agent.

Configuration is stored in the user profile (`%APPDATA%` on Windows or `~/Library/Application Support` on macOS), never in the repository. Inspect it with `confluence-publish config show`.

## Usage

Publish one file, creating the page when it does not exist and updating it otherwise:

```bash
confluence-publish my-file.md
```

Explicit options override frontmatter and saved configuration:

```bash
confluence-publish my-file.md --space DEV --parent "Technical Documentation" --title "My title"
```

Publish a flat directory of `.md` files under the same parent:

```bash
confluence-publish publish --dir ./docs --recursive
```

### Publish a directory tree (`--tree`)

Reproduce a local directory structure as a Confluence page hierarchy:

```bash
confluence-publish publish --dir ./architecture --tree --space PRJ2043 --parent 447945799
```

Rules:

- The directory supplied to `--dir` does not create a page of its own. Its `.md` files become direct children of `--parent`.
- A subdirectory becomes a page when it contains Markdown content:
  1. When `_index.md` or `index.md` exists, that file provides the page title and content.
  2. Otherwise, when at least one `.md` file exists directly in the directory, the tool creates a **container** page. Its title is derived from the directory name (hyphens and underscores become spaces), and a native Confluence children macro supplies its content. Each Markdown file becomes a child page of that container.
- A directory with no Markdown files, either directly or in an index file, does not create a page. Markdown found deeper in its subdirectories is attached to the current parent.
- Files and directories are processed together in alphabetical order. Prefix names with two-digit numbers (`01-`, `02-`, … `10-`) for predictable order. Keeping the numeric prefix in the page title is the most reliable way to preserve visual order because Confluence does not always guarantee API insertion order.

Example local tree:

```text
architecture/
  01-target-architecture-proposal/
    new-interoperability-v2.md      -> child of the container page
    imgs/                           -> no .md: attachments only, no page
  02-communications-and-routing/
    routing-plan.md                 -> child of the container page
  03-security/
    _index.md                       -> page content, no separate container
    01-authentication.md            -> child of "03 Security"
    02-encryption.md                -> child of "03 Security"
  04-sizing/                        -> empty directory: no page
```

Always test first with `--dry-run`. Each displayed page includes its planned parent in brackets so that the hierarchy can be checked before anything is sent to Confluence:

```bash
confluence-publish my-file.md --dry-run
```

### Frontmatter (optional)

A YAML block at the start of a Markdown file can control publication without requiring flags on every run:

```markdown
---
title: My page
space: DEV
parent: 123456          # ID or title of an existing page
page_id: 987654         # optional: force an update of this exact page
labels: [architecture, backend]
---

# Page content
```

Resolution precedence is CLI flag > frontmatter > saved configuration. When no title is specified, it is derived from the filename.

## Known limitations

- Raw HTML in Markdown is ignored instead of copied, preventing unexpected content from being injected into the page.
- Markdown links to other `.md` files are published unchanged and are not converted automatically into Confluence page links.
- Mermaid rendering requires a locally installed Chrome, Chromium, or Edge. Without one, diagrams are inserted as raw code blocks with a warning.
- Mermaid attachment names changed from `.png` to `.svg`. Republishing a page created by an older version does not delete unused PNG attachments; remove them manually from the Confluence attachment list if needed.
- Unlike strict CommonMark, a single newline in the source becomes a visible line break (`<br/>`) in Confluence. Leave an empty line between text blocks to create a paragraph break.

## Project structure

```text
cmd/confluence-publish/   CLI entry point
internal/frontmatter/     YAML frontmatter extraction
internal/mdconvert/       Markdown to Confluence storage-format conversion
internal/mermaid/         Mermaid to SVG rendering through headless Chrome
internal/confluence/      REST API v1 client for pages, attachments, and labels
internal/config/          persistent user-profile configuration
examples/                 example Markdown files
scripts/                  build and Mermaid dependency scripts
```
