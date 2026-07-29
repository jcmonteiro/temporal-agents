package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Template is a reusable run/schedule invocation saved by the user.
// WorkDir is intentionally not stored: templates run in the current directory.
type Template struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`           // "run" or "schedule"
	Spec   string `json:"spec,omitempty"` // interval/cron, only for "schedule"
	Prompt string `json:"prompt"`
	Chain  bool   `json:"chain,omitempty"` // re-trigger the workflow on success
}

// Config is the on-disk template store.
type Config struct {
	Templates []Template `json:"templates"`
}

// configPath returns <user config dir>/temporal-agents/templates.json.
func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate user config dir: %w", err)
	}
	return filepath.Join(dir, "temporal-agents", "templates.json"), nil
}

// worktreesDir returns <user config dir>/temporal-agents/worktrees, the base
// directory under which `code develop --worktree` creates a per-branch git
// worktree.
func worktreesDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate user config dir: %w", err)
	}
	return filepath.Join(dir, "temporal-agents", "worktrees"), nil
}

func loadConfig() (*Config, string, error) {
	path, err := configPath()
	if err != nil {
		return nil, "", err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Config{}, path, nil
	}
	if err != nil {
		return nil, path, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, path, fmt.Errorf("parse %s: %w", path, err)
	}
	return &cfg, path, nil
}

func saveConfig(cfg *Config, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// saveTemplate inserts or replaces a template by name and persists the store.
func saveTemplate(t Template) (string, error) {
	cfg, path, err := loadConfig()
	if err != nil {
		return path, err
	}
	replaced := false
	for i := range cfg.Templates {
		if cfg.Templates[i].Name == t.Name {
			cfg.Templates[i] = t
			replaced = true
			break
		}
	}
	if !replaced {
		cfg.Templates = append(cfg.Templates, t)
	}
	return path, saveConfig(cfg, path)
}

func getTemplate(name string) (Template, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return Template{}, err
	}
	for _, t := range cfg.Templates {
		if t.Name == name {
			return t, nil
		}
	}
	return Template{}, fmt.Errorf("no template named %q", name)
}

func deleteTemplate(name string) (string, error) {
	cfg, path, err := loadConfig()
	if err != nil {
		return path, err
	}
	kept := cfg.Templates[:0]
	found := false
	for _, t := range cfg.Templates {
		if t.Name == name {
			found = true
			continue
		}
		kept = append(kept, t)
	}
	if !found {
		return path, fmt.Errorf("no template named %q", name)
	}
	cfg.Templates = kept
	return path, saveConfig(cfg, path)
}
