// Author: Emmanuel COLUSSI
// Copyright (c) 2026 Emmanuel COLUSSI
// SPDX-License-Identifier: MIT
//
// Package frontmatter extracts and parses a YAML frontmatter block placed at
// the beginning of a Markdown file and delimited by "---" lines.
package frontmatter

import (
	"bytes"
	"strings"

	"github.com/goccy/go-yaml"
)

// Meta contains optional metadata that controls Confluence publication
// without requiring every value to be passed as a CLI flag.
//
// Example:
//
//	---
//	title: My page
//	space: DEV
//	parent: 123456
//	page_id: 987654
//	labels: [architecture, backend]
//	---
type Meta struct {
	Title  string   `yaml:"title"`
	Space  string   `yaml:"space"`
	Parent string   `yaml:"parent"`  // parent page title or ID
	PageID string   `yaml:"page_id"` // when known, forces an update of this page
	Labels []string `yaml:"labels"`
}

// Split separates optional YAML frontmatter from the remaining Markdown.
// When no frontmatter is present, it returns a zero-value Meta and body == input.
func Split(input []byte) (meta Meta, body []byte, err error) {
	body = input
	trimmed := bytes.TrimLeft(input, "\ufeff \t\r\n")
	if !bytes.HasPrefix(trimmed, []byte("---")) {
		return meta, body, nil
	}

	// Normalize line endings to simplify delimiter detection.
	text := string(trimmed)
	// The first line must be exactly "---".
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
		// Treat a missing closing delimiter as regular content instead of failing.
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
