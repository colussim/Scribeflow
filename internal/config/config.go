// Package config gère la configuration persistée (URL Confluence,
// identifiants, chemin Chrome) dans un fichier JSON du profil utilisateur,
// commun à Windows et macOS.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config est l'ensemble des paramètres réutilisables entre invocations.
type Config struct {
	BaseURL    string `json:"base_url"`
	Token      string `json:"token,omitempty"`
	Username   string `json:"username,omitempty"`
	Password   string `json:"password,omitempty"`
	Insecure   bool   `json:"insecure,omitempty"`
	ChromePath string `json:"chrome_path,omitempty"`
	Space      string `json:"default_space,omitempty"`
	Parent     string `json:"default_parent,omitempty"`
}

// Path retourne l'emplacement standard du fichier de config:
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

// Load lit la config depuis disque. Si le fichier n'existe pas, retourne
// une Config vide sans erreur.
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

// Save écrit la config sur disque (permissions restreintes, 0600) en
// créant le répertoire parent si besoin.
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
