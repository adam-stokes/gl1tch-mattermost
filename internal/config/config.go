// Package config manages the plugin's persisted configuration at
// ~/.config/glitch/plugins/mattermost.yaml.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config holds Mattermost connection settings.
type Config struct {
	URL   string `yaml:"url"`
	Token string `yaml:"token"`
}

func path() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("config: cannot determine config dir: %w", err)
	}
	dir := filepath.Join(cfg, "glitch", "plugins")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("config: mkdir: %w", err)
	}
	return filepath.Join(dir, "mattermost.yaml"), nil
}

// Load reads the config file. Returns a zero Config if it doesn't exist.
func Load() (Config, error) {
	p, err := path()
	if err != nil {
		return Config{}, err
	}
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return Config{}, fmt.Errorf("config: parse: %w", err)
	}
	return c, nil
}

// Save writes the config file.
func Save(c Config) (string, error) {
	p, err := path()
	if err != nil {
		return "", err
	}
	b, err := yaml.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("config: marshal: %w", err)
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		return "", fmt.Errorf("config: write: %w", err)
	}
	return p, nil
}
