// Package frontmatter extrait et parse un bloc YAML d'en-tête (frontmatter)
// placé au début d'un fichier Markdown, délimité par des lignes "---".
package frontmatter

import (
	"bytes"
	"strings"

	"github.com/goccy/go-yaml"
)

// Meta représente les métadonnées optionnelles qu'on peut placer en tête
// d'un fichier markdown pour piloter la publication Confluence sans avoir
// à tout passer en flags CLI.
//
// Exemple:
//
//	---
//	title: Ma page
//	space: DEV
//	parent: 123456
//	page_id: 987654
//	labels: [architecture, backend]
//	---
type Meta struct {
	Title  string   `yaml:"title"`
	Space  string   `yaml:"space"`
	Parent string   `yaml:"parent"`  // titre OU id de la page parente
	PageID string   `yaml:"page_id"` // si connu, force une mise à jour de cette page
	Labels []string `yaml:"labels"`
}

// Split sépare le frontmatter YAML (s'il existe) du reste du contenu markdown.
// Si aucun frontmatter n'est présent, Meta est retourné à zéro et body == input.
func Split(input []byte) (meta Meta, body []byte, err error) {
	body = input
	trimmed := bytes.TrimLeft(input, "\ufeff \t\r\n")
	if !bytes.HasPrefix(trimmed, []byte("---")) {
		return meta, body, nil
	}

	// Normalise les fins de ligne pour simplifier la recherche du délimiteur.
	text := string(trimmed)
	// La première ligne doit être exactement "---"
	lines := strings.SplitN(text, "\n", -1)
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r") != "---" {
		return meta, body, nil
	}

	closeIdx := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r") == "---" {
			closeIdx = i
			break
		}
	}
	if closeIdx == -1 {
		// Pas de délimiteur fermant trouvé, on considère qu'il n'y a pas
		// de frontmatter plutôt que d'échouer.
		return meta, body, nil
	}

	yamlBlock := strings.Join(lines[1:closeIdx], "\n")
	rest := strings.Join(lines[closeIdx+1:], "\n")

	if strings.TrimSpace(yamlBlock) != "" {
		if err := yaml.Unmarshal([]byte(yamlBlock), &meta); err != nil {
			return meta, input, err
		}
	}

	return meta, []byte(rest), nil
}
