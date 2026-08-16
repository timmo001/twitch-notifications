package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

var currentConfigPath string

type channelsFile struct {
	WatchedChannels []WatchedChannel `yaml:"watched_channels"`
}

// WatchedChannel represents a watched channel that can be specified as either a string or an object
type WatchedChannel struct {
	Name string
	Open *bool // nil means not specified (defaults to false), true/false means explicitly set
}

// UnmarshalYAML implements custom YAML unmarshaling to support both string and object notation
// Accepts both legacy formats for backward compatibility:
//   - String: `- channelname`
//   - Object (legacy): `- channelname: { open: true }`
//   - Object (standard): `- name: channelname; open: true`
func (wc *WatchedChannel) UnmarshalYAML(unmarshal func(interface{}) error) error {
	// Try to unmarshal as a string first (legacy format)
	var str string
	if err := unmarshal(&str); err == nil {
		wc.Name = str
		open := false
		wc.Open = &open // Default to false
		return nil
	}

	// Try standard object format: { name: "...", open: true/false/null }
	var standardObj struct {
		Name string `yaml:"name"`
		Open *bool  `yaml:"open"`
	}
	if err := unmarshal(&standardObj); err == nil && standardObj.Name != "" {
		wc.Name = standardObj.Name
		if standardObj.Open == nil {
			// Convert null to false
			open := false
			wc.Open = &open
		} else {
			wc.Open = standardObj.Open
		}
		return nil
	}

	// Try legacy object format: { channelname: { open: true } }
	var m map[string]interface{}
	if err := unmarshal(&m); err != nil {
		return err
	}

	// Extract channel name and open property from legacy format
	open := false // Default to false
	for name, val := range m {
		wc.Name = name
		if openMap, ok := val.(map[string]interface{}); ok {
			if openVal, exists := openMap["open"]; exists {
				if openBool, ok := openVal.(bool); ok {
					open = openBool
				}
			}
		}
		break // Only process first key-value pair
	}
	wc.Open = &open

	return nil
}

// MarshalYAML implements custom YAML marshaling to always output in the standard format
// Always outputs as: { name: "...", open: true/false } (never null)
func (wc WatchedChannel) MarshalYAML() (interface{}, error) {
	open := false
	if wc.Open != nil {
		open = *wc.Open
	}
	return map[string]interface{}{
		"name": wc.Name,
		"open": open, // Always true or false, never null
	}, nil
}

// Config represents the application configuration
type Config struct {
	WatchedChannels []WatchedChannel `yaml:"watched_channels"`
	NotifyOnStartup bool             `yaml:"notify_on_startup"`
	SoundFile       string           `yaml:"sound_file"`       // Optional path to sound file to play with notifications
	PollInterval    int              `yaml:"poll_interval"`    // Polling interval in seconds for overflow channels (default: 60)
	PeriodicRestart *bool            `yaml:"periodic_restart"` // Restart the application every hour (default: true)
	Twitch          TwitchConfig     `yaml:"twitch"`
}

// GetPollInterval returns the poll interval duration, defaulting to 60 seconds if not set
func (c *Config) GetPollInterval() int {
	if c.PollInterval <= 0 {
		return 60 // Default to 60 seconds
	}
	return c.PollInterval
}

// ShouldPeriodicRestart returns whether the application should restart periodically (default: true)
func (c *Config) ShouldPeriodicRestart() bool {
	if c.PeriodicRestart == nil {
		return true // Default to true
	}
	return *c.PeriodicRestart
}

// TwitchConfig contains Twitch API credentials
type TwitchConfig struct {
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
	AccessToken  string `yaml:"access_token"`  // Optional if refresh_token is provided
	RefreshToken string `yaml:"refresh_token"` // Optional, used to obtain access token
}

// Load reads and parses the configuration file
// If the config file doesn't exist, it will copy from config.example.yaml
func Load(configPath string) (*Config, error) {
	// Store the config path for later use
	currentConfigPath = configPath

	// Ensure the config directory exists
	configDir := filepath.Dir(configPath)
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create config directory: %w", err)
	}

	// Check if config file exists
	createdConfigFromExample := false
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// Try to copy from example file
		examplePath := getExamplePath("config.example.yaml")
		if err := copyExampleConfig(examplePath, configPath); err != nil {
			return nil, fmt.Errorf("config file not found and failed to create from example: %w\nPlease copy %s to %s manually", err, examplePath, configPath)
		}
		fmt.Printf("Created config file from example: %s\n", configPath)
		createdConfigFromExample = true
	}

	channelsPath := getChannelsPath(configPath)
	if createdConfigFromExample {
		if _, err := os.Stat(channelsPath); os.IsNotExist(err) {
			examplePath := getExamplePath("channels.example.yml")
			if err := copyExampleConfig(examplePath, channelsPath); err != nil {
				return nil, fmt.Errorf("channels file not found and failed to create from example: %w\nPlease copy %s to %s manually", err, examplePath, channelsPath)
			}
			fmt.Printf("Created channels file from example: %s\n", channelsPath)
		}
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	if err := loadWatchedChannels(channelsPath, &config); err != nil {
		return nil, err
	}

	// Expand environment variables
	config.Twitch.ClientID = expandEnv(config.Twitch.ClientID)
	config.Twitch.ClientSecret = expandEnv(config.Twitch.ClientSecret)
	config.Twitch.AccessToken = expandEnv(config.Twitch.AccessToken)
	config.Twitch.RefreshToken = expandEnv(config.Twitch.RefreshToken)
	config.SoundFile = expandEnv(config.SoundFile)

	return &config, nil
}

// GetConfigPath returns the path to the currently loaded configuration file
func GetConfigPath() string {
	return currentConfigPath
}

// GetChannelsPath returns the path to the channels file for the currently loaded config
func GetChannelsPath() string {
	if currentConfigPath == "" {
		return ""
	}
	return getChannelsPath(currentConfigPath)
}

func getChannelsPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "channels.yml")
}

func loadWatchedChannels(channelsPath string, cfg *Config) error {
	data, err := os.ReadFile(channelsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read channels file: %w", err)
	}

	var channels channelsFile
	if err := yaml.Unmarshal(data, &channels); err != nil {
		return fmt.Errorf("failed to parse channels file: %w", err)
	}

	cfg.WatchedChannels = channels.WatchedChannels
	return nil
}

// getExamplePath finds the example config file in the application directory
// It searches in order:
// 1. Relative to the executable (for installed binaries)
// 2. Relative to the current working directory (for development)
// 3. In common application paths
func getExamplePath(exampleFile string) string {
	// Try 1: Relative to executable
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidate := filepath.Join(exeDir, "config", exampleFile)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		// Also try in parent directory of executable
		parentDir := filepath.Dir(exeDir)
		candidate = filepath.Join(parentDir, "config", exampleFile)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	// Try 2: Relative to current working directory (for development)
	if wd, err := os.Getwd(); err == nil {
		candidate := filepath.Join(wd, "config", exampleFile)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	// Try 3: Common application paths (fallback)
	// This is a last resort and may not work, but we'll return it anyway
	return filepath.Join("config", exampleFile)
}

// copyExampleConfig copies the example config file to the target path
func copyExampleConfig(examplePath, targetPath string) error {
	// Check if example file exists
	if _, err := os.Stat(examplePath); os.IsNotExist(err) {
		return fmt.Errorf("example config file not found: %s", examplePath)
	}

	// Open example file
	src, err := os.Open(examplePath)
	if err != nil {
		return fmt.Errorf("failed to open example config: %w", err)
	}
	defer src.Close()

	// Create target file
	dst, err := os.Create(targetPath)
	if err != nil {
		return fmt.Errorf("failed to create config file: %w", err)
	}
	defer dst.Close()

	// Copy contents
	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("failed to copy config file: %w", err)
	}

	return nil
}

// expandEnv replaces ${VAR} or $VAR with environment variable values
func expandEnv(s string) string {
	return os.ExpandEnv(s)
}

// ShouldAutoOpen checks if a channel should be automatically opened in the browser
// Returns true only if the channel is watched AND has open: true explicitly set
func (c *Config) ShouldAutoOpen(channelName string) bool {
	for _, wc := range c.WatchedChannels {
		if strings.EqualFold(wc.Name, channelName) {
			return wc.Open != nil && *wc.Open
		}
	}
	return false
}
