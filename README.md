# confluence-publish

Un petit outil en ligne de commande (Go, un seul binaire, Windows + macOS)
qui publie directement des fichiers Markdown comme pages Confluence via
l'API REST — sans passer par un copier-coller dans l'éditeur.

Ce que ça résout :

- Le rendu markdown → Confluence est fait correctement (titres, listes,
  tableaux, code, citations, listes de tâches...), pas approximé par un
  copier-coller depuis un éditeur markdown.
- Les diagrammes **Mermaid** (` ```mermaid `) sont rendus en image PNG
  localement (via Chrome/Edge headless, 100% hors-ligne) et uploadés comme
  pièce jointe — donc affichés correctement même si votre Confluence n'a
  pas de plugin Mermaid installé.
- Les **images locales** référencées dans le markdown (`![alt](./img.png)`)
  sont automatiquement uploadées comme pièces jointes et liées dans la
  page.
- Relancer l'outil sur le même fichier **met à jour** la page existante
  (au lieu d'en créer une nouvelle), pièces jointes comprises.

Prérequis Confluence : **Server / Data Center** (API REST v1). Un
navigateur Chrome, Chromium ou Edge doit être installé sur votre poste
pour le rendu des diagrammes Mermaid (déjà présent par défaut sur la
plupart des postes Windows/Mac) — sinon utilisez `--no-mermaid`.

## Installation

Récupérez le binaire correspondant à votre poste (dossier `dist/`) et
placez-le où vous voulez, par exemple dans un dossier de votre `PATH` :

- Windows : `confluence-publish-windows-amd64.exe`
- macOS (Apple Silicon, M1/M2/M3/...) : `confluence-publish-darwin-arm64`
- macOS (Intel) : `confluence-publish-darwin-amd64`

Sur macOS, la première exécution peut demander une autorisation Gatekeeper
(clic droit → Ouvrir, ou `xattr -d com.apple.quarantine <binaire>`).

Pour recompiler soi-même (nécessite Go >= 1.21) :

```bash
./scripts/fetch-mermaid.sh   # télécharge mermaid.min.js (une fois)
./scripts/build.sh           # génère les 3 binaires dans dist/
```

## Configuration

Créez d'abord un **Personal Access Token** dans Confluence (menu profil →
Paramètres du compte → Personal Access Tokens, sur Server/Data Center),
puis :

```bash
confluence-publish config set \
  --base-url https://confluence.monentreprise.com \
  --token VOTRE_PAT \
  --space DEV
```

`--space` fixe l'espace Confluence par défaut (peut être surchargé par
fichier). Options utiles supplémentaires :

- `--insecure` : ignore la vérification du certificat TLS (utile pour une
  instance interne en certificat auto-signé).
- `--username` / `--password` : authentification basique, si votre
  instance n'accepte pas les PAT.
- `--chrome-path` : chemin explicite vers Chrome/Chromium/Edge si l'outil
  ne le détecte pas automatiquement.
- `--parent` : page parente par défaut (titre ou id).

La config est stockée dans votre profil utilisateur (`%APPDATA%` sous
Windows, `~/Library/Application Support` sous macOS), jamais dans le
dépôt. Vérifiez avec `confluence-publish config show`.

## Utilisation

Publier un fichier (crée la page si elle n'existe pas, sinon la met à jour) :

```bash
confluence-publish mon-fichier.md
```

Avec des options explicites (remplacent le frontmatter/la config) :

```bash
confluence-publish mon-fichier.md --space DEV --parent "Documentation technique" --title "Mon titre"
```

Publier tout un dossier de fichiers `.md` :

```bash
confluence-publish publish --dir ./docs --recursive
```

Voir le résultat sans rien envoyer à Confluence :

```bash
confluence-publish mon-fichier.md --dry-run
```

### Frontmatter (optionnel)

En tête de fichier markdown, un bloc YAML permet de piloter la
publication sans taper de flags à chaque fois :

```markdown
---
title: Ma page
space: DEV
parent: 123456          # id OU titre d'une page existante
page_id: 987654          # optionnel : force la mise à jour de cette page précise
labels: [architecture, backend]
---

# Contenu de la page...
```

Priorité de résolution : flag CLI > frontmatter > config sauvegardée. Le
titre, à défaut, est dérivé du nom de fichier.

## Limitations connues

- HTML brut dans le markdown est ignoré (non converti) plutôt que copié
  tel quel, pour éviter d'injecter du contenu imprévisible dans la page.
- Les liens markdown vers d'autres fichiers `.md` du dépôt ne sont pas
  automatiquement transformés en liens inter-pages Confluence : ils sont
  publiés tels quels.
- Le rendu Mermaid dépend d'un Chrome/Chromium/Edge installé localement ;
  sans navigateur détecté, le diagramme est inséré en bloc de code brut
  (avec un message d'avertissement) plutôt que d'échouer la publication.

## Structure du projet

```
cmd/confluence-publish/   point d'entrée CLI
internal/frontmatter/     extraction du bloc YAML en tête de fichier
internal/mdconvert/       markdown -> storage format Confluence (XHTML)
internal/mermaid/         rendu mermaid -> PNG via Chrome headless
internal/confluence/      client REST API v1 (pages, pièces jointes, labels)
internal/config/          configuration persistée (profil utilisateur)
examples/                 fichier markdown d'exemple
scripts/                  scripts de build et de récupération de mermaid.js
```
