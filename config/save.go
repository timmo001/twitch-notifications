package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type persistedConfig struct {
	NotifyOnStartup bool         `yaml:"notify_on_startup"`
	SoundFile       string       `yaml:"sound_file"`
	PollInterval    int          `yaml:"poll_interval"`
	PeriodicRestart *bool        `yaml:"periodic_restart"`
	Twitch          TwitchConfig `yaml:"twitch"`
}

// Save writes the configuration to a file
func Save(cfg *Config, configPath string) error {
	// Ensure the config directory exists
	configDir := filepath.Dir(configPath)
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	if err := writeChannelsFile(getChannelsPath(configPath), cfg.WatchedChannels); err != nil {
		return err
	}

	if err := writeConfigFile(configPath, cfg); err != nil {
		return err
	}

	return nil
}

func writeConfigFile(configPath string, cfg *Config) error {
	data, err := yaml.Marshal(persistedConfig{
		NotifyOnStartup: cfg.NotifyOnStartup,
		SoundFile:       cfg.SoundFile,
		PollInterval:    cfg.PollInterval,
		PeriodicRestart: cfg.PeriodicRestart,
		Twitch:          cfg.Twitch,
	})
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

func writeChannelsFile(channelsPath string, watchedChannels []WatchedChannel) error {
	channelsDir := filepath.Dir(channelsPath)
	if err := os.MkdirAll(channelsDir, 0755); err != nil {
		return fmt.Errorf("failed to create channels directory: %w", err)
	}

	data, err := yaml.Marshal(channelsFile{WatchedChannels: watchedChannels})
	if err != nil {
		return fmt.Errorf("failed to marshal channels: %w", err)
	}
	data = append([]byte("---\n"), data...)

	if err := os.WriteFile(channelsPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write channels file: %w", err)
	}

	return nil
}

// SaveTokens updates only the access_token and refresh_token fields in the existing config file
// Uses YAML library to properly format the output
func SaveTokens(configPath string, accessToken, refreshToken string) error {
	// Load existing config
	cfg, err := Load(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Update tokens
	cfg.UpdateTokens(accessToken, refreshToken)

	channelsPath := getChannelsPath(configPath)
	if _, err := os.Stat(channelsPath); os.IsNotExist(err) && len(cfg.WatchedChannels) > 0 {
		if err := writeChannelsFile(channelsPath, cfg.WatchedChannels); err != nil {
			return fmt.Errorf("failed to save channels: %w", err)
		}
	}

	// Save only the mutable token-bearing config file.
	if err := writeConfigFile(configPath, cfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	return nil
}

// UpdateTokens updates the access token and refresh token in the config
func (c *Config) UpdateTokens(accessToken, refreshToken string) {
	c.Twitch.AccessToken = accessToken
	if refreshToken != "" {
		c.Twitch.RefreshToken = refreshToken
	}
}
