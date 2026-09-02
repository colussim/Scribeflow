// Author: Emmanuel COLUSSI
// Copyright (c) 2026 Emmanuel COLUSSI
// SPDX-License-Identifier: MIT

// Package config manages persistent settings (Confluence URL, credentials,
// and Chrome path) in a user-profile JSON file on Windows and macOS.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config contains settings reused across invocations.
type Config struct {
	BaseURL    string `json:"base_url"`
	Token      string `json:"token,omitempty"`
	Username   string `json:"username,omitempty"`
	Password   string `json:"password,omitempty"`
	Insecure   bool   `json:"insecure,omitempty"`
	ChromePath string `json:"chrome_path,omitempty"`
	Space      string `json:"default_space,omitempty"`
	Parent     string `json:"default_parent,omitempty"`
	UserAgent  string `json:"user_agent,omitempty"`
}

// Path returns the standard configuration file location:
//   - Windows: %APPDATA%\confluence-publish\config.json
//   - macOS:   ~/Library/Application Support/confluence-publish/config.json
//   - Linux:   ~/.config/confluence-publish/config.json
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "confluence-publish", "config.json"), nil
}

// Load reads the configuration from disk. A missing file returns an empty
// Config without an error.
func Load() (Config, error) {
	var c Config
	path, err := Path()
	if err != nil {
		return c, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return c, err
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return c, err
	}
	return c, nil
}

// Save writes the configuration with restricted permissions (0600), creating
// its parent directory when necessary.
func Save(c Config) error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
