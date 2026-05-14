package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type cliConfig struct {
	CurrentProfile string                `json:"current_profile,omitempty"`
	Profiles       map[string]cliProfile `json:"profiles,omitempty"`
}

type cliProfile struct {
	Server string `json:"server"`
	Token  string `json:"token"`
}

func loadCLIConfig() (cliConfig, error) {
	path, err := cliConfigPath()
	if err != nil {
		return cliConfig{}, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cliConfig{Profiles: map[string]cliProfile{}}, nil
	}
	if err != nil {
		return cliConfig{}, err
	}
	var cfg cliConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cliConfig{}, fmt.Errorf("read CLI config %s: %w", path, err)
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]cliProfile{}
	}
	return cfg, nil
}

func saveCLIConfig(cfg cliConfig) error {
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]cliProfile{}
	}
	path, err := cliConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(path, raw, 0o600)
}

func cliConfigPath() (string, error) {
	if path := strings.TrimSpace(os.Getenv("SUPERCDN_CONFIG")); path != "" {
		return path, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "supercdn", "cli.json"), nil
}

func saveCLIProfile(profileName, serverURL, token string) error {
	cfg, err := loadCLIConfig()
	if err != nil {
		return err
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]cliProfile{}
	}
	cfg.CurrentProfile = profileName
	cfg.Profiles[profileName] = cliProfile{Server: strings.TrimRight(serverURL, "/"), Token: token}
	return saveCLIConfig(cfg)
}
