package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

var saveMu sync.Mutex

type persistedConfig struct {
	NotifyOnStartup bool         `yaml:"notify_on_startup"`
	SoundFile       string       `yaml:"sound_file"`
	PollInterval    int          `yaml:"poll_interval"`
	PeriodicRestart *bool        `yaml:"periodic_restart"`
	SystemTray      *bool        `yaml:"system_tray,omitempty"`
	Twitch          TwitchConfig `yaml:"twitch"`
}

// Save writes the configuration to a file
func Save(cfg *Config, configPath string) error {
	saveMu.Lock()
	defer saveMu.Unlock()

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
		SystemTray:      cfg.SystemTray,
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
	saveMu.Lock()
	defer saveMu.Unlock()

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

// SaveSystemTray changes tray visibility without rewriting channels or expanding credentials.
func SaveSystemTray(configPath string, enabled bool) error {
	saveMu.Lock()
	defer saveMu.Unlock()

	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read config: %w", err)
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return fmt.Errorf("failed to parse config: %w", err)
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("config must contain a YAML mapping")
	}
	root := document.Content[0]
	value := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: fmt.Sprint(enabled)}
	found := false
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value == "system_tray" {
			root.Content[i+1].Tag = value.Tag
			root.Content[i+1].Value = value.Value
			root.Content[i+1].Kind = value.Kind
			found = true
			break
		}
	}
	if !found {
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "system_tray"}, value)
	}
	data, err = yaml.Marshal(&document)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
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
