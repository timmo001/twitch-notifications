package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"twitch-notifications/config"

	"github.com/manifoldco/promptui"
)

func handleCLICommand(args []string, defaultConfigPath string) (bool, error) {
	filteredArgs, configPath, err := extractConfigPath(args, defaultConfigPath)
	if err != nil {
		return true, err
	}

	if len(filteredArgs) == 0 {
		return false, nil
	}

	switch filteredArgs[0] {
	case "channel":
		return true, handleChannelCommand(filteredArgs[1:], configPath)
	case "serve":
		// Explicit server mode — skip TTY detection and run the server directly
		return false, nil
	case "tui":
		// Explicit TUI launch
		return true, launchTUI()
	default:
		return false, nil
	}
}

func extractConfigPath(args []string, defaultConfigPath string) ([]string, string, error) {
	filteredArgs := make([]string, 0, len(args))
	configPath := defaultConfigPath

	for i := 0; i < len(args); i++ {
		arg := args[i]

		switch {
		case arg == "-config" || arg == "--config":
			if i+1 >= len(args) {
				return nil, "", fmt.Errorf("missing value for %s", arg)
			}
			configPath = args[i+1]
			i++
		case strings.HasPrefix(arg, "-config="):
			configPath = strings.TrimPrefix(arg, "-config=")
		case strings.HasPrefix(arg, "--config="):
			configPath = strings.TrimPrefix(arg, "--config=")
		default:
			filteredArgs = append(filteredArgs, arg)
		}
	}

	return filteredArgs, configPath, nil
}

func handleChannelCommand(args []string, configPath string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing channel subcommand\n\n%s", channelCommandUsage())
	}

	switch args[0] {
	case "add":
		return handleChannelAddCommand(args[1:], configPath)
	case "remove":
		return handleChannelRemoveCommand(args[1:], configPath)
	default:
		return fmt.Errorf("unknown channel subcommand %q\n\n%s", args[0], channelCommandUsage())
	}
}

func handleChannelAddCommand(args []string, configPath string) error {
	channelName, hasChannelName, openValue, hasOpenValue, err := parseChannelAddArgs(args)
	if err != nil {
		return err
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if !hasChannelName {
		channelName, err = promptForChannelName("")
		if err != nil {
			return err
		}
	}

	channelName = strings.TrimSpace(channelName)
	if channelName == "" {
		return fmt.Errorf("channel name is required")
	}

	existingIndex := findWatchedChannelIndex(cfg.WatchedChannels, channelName)
	defaultOpen := false
	if existingIndex >= 0 && cfg.WatchedChannels[existingIndex].Open != nil {
		defaultOpen = *cfg.WatchedChannels[existingIndex].Open
	}

	if !hasOpenValue {
		openValue, err = promptForChannelOpen(defaultOpen)
		if err != nil {
			return err
		}
	}

	updated := upsertWatchedChannel(&cfg.WatchedChannels, channelName, openValue)
	if err := config.Save(cfg, configPath); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	channelsPath := filepath.Join(filepath.Dir(configPath), "channels.yml")
	if updated {
		fmt.Printf("Updated channel %q (open=%t) in %s\n", channelName, openValue, channelsPath)
	} else {
		fmt.Printf("Added channel %q (open=%t) to %s\n", channelName, openValue, channelsPath)
	}

	if err := requestRunningInstanceRestart(); err != nil {
		return err
	}

	return nil
}

func handleChannelRemoveCommand(args []string, configPath string) error {
	channelName, err := parseChannelRemoveArgs(args)
	if err != nil {
		return err
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if len(cfg.WatchedChannels) == 0 {
		return fmt.Errorf("no watched channels configured in %s", filepath.Join(filepath.Dir(configPath), "channels.yml"))
	}

	if channelName == "" {
		channelName, err = promptForChannelRemoval(cfg.WatchedChannels)
		if err != nil {
			return err
		}
	}

	channelName = strings.TrimSpace(channelName)
	if channelName == "" {
		return fmt.Errorf("channel name is required")
	}

	removedChannel, removed := removeWatchedChannel(&cfg.WatchedChannels, channelName)
	if !removed {
		return fmt.Errorf("channel %q not found", channelName)
	}

	if err := config.Save(cfg, configPath); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	channelsPath := filepath.Join(filepath.Dir(configPath), "channels.yml")
	fmt.Printf("Removed channel %q from %s\n", removedChannel.Name, channelsPath)

	if err := requestRunningInstanceRestart(); err != nil {
		return err
	}

	return nil
}

func requestRunningInstanceRestart() error {
	if err := sendRestartCommand(false); err != nil {
		if isServiceUnavailableDBusError(err) {
			return nil
		}
		return fmt.Errorf("failed to restart running instance: %w", err)
	}

	fmt.Println("Requested running instance restart to apply channel changes")
	return nil
}

func isServiceUnavailableDBusError(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()
	return strings.Contains(errStr, "org.freedesktop.DBus.Error.ServiceUnknown") ||
		strings.Contains(errStr, "org.freedesktop.DBus.Error.UnknownObject")
}

func parseChannelAddArgs(args []string) (string, bool, bool, bool, error) {
	if len(args) > 2 {
		return "", false, false, false, fmt.Errorf("too many arguments for channel add\n\n%s", channelAddUsage())
	}

	var channelName string
	var hasChannelName bool
	var openValue bool
	var hasOpenValue bool

	for _, arg := range args {
		if parsedBool, ok := parseStrictBool(arg); ok {
			if hasOpenValue {
				return "", false, false, false, fmt.Errorf("received multiple open values\n\n%s", channelAddUsage())
			}
			openValue = parsedBool
			hasOpenValue = true
			continue
		}

		if hasChannelName {
			return "", false, false, false, fmt.Errorf("received multiple channel names\n\n%s", channelAddUsage())
		}

		channelName = arg
		hasChannelName = true
	}

	return channelName, hasChannelName, openValue, hasOpenValue, nil
}

func parseChannelRemoveArgs(args []string) (string, error) {
	if len(args) > 1 {
		return "", fmt.Errorf("too many arguments for channel remove\n\n%s", channelRemoveUsage())
	}

	if len(args) == 0 {
		return "", nil
	}

	if _, ok := parseStrictBool(args[0]); ok {
		return "", fmt.Errorf("channel remove expects a channel name, not a boolean\n\n%s", channelRemoveUsage())
	}

	return args[0], nil
}

func parseStrictBool(value string) (bool, bool) {
	switch {
	case strings.EqualFold(value, "true"):
		return true, true
	case strings.EqualFold(value, "false"):
		return false, true
	default:
		return false, false
	}
}

func promptForChannelName(defaultValue string) (string, error) {
	prompt := promptui.Prompt{
		Label:   "Channel name",
		Default: defaultValue,
		Validate: func(input string) error {
			if strings.TrimSpace(input) == "" {
				return fmt.Errorf("channel name is required")
			}
			return nil
		},
	}

	result, err := prompt.Run()
	if err != nil {
		return "", fmt.Errorf("channel prompt failed: %w", err)
	}

	return strings.TrimSpace(result), nil
}

func promptForChannelOpen(defaultValue bool) (bool, error) {
	items := []string{"false", "true"}
	defaultIndex := 0
	if defaultValue {
		defaultIndex = 1
	}

	prompt := promptui.Select{
		Label:     "Open stream automatically when live?",
		Items:     items,
		Size:      len(items),
		CursorPos: defaultIndex,
	}

	_, result, err := prompt.Run()
	if err != nil {
		return false, fmt.Errorf("open prompt failed: %w", err)
	}

	openValue, _ := parseStrictBool(result)
	return openValue, nil
}

func promptForChannelRemoval(watchedChannels []config.WatchedChannel) (string, error) {
	items := make([]string, 0, len(watchedChannels))
	for _, watchedChannel := range watchedChannels {
		items = append(items, formatWatchedChannelOption(watchedChannel))
	}

	prompt := promptui.Select{
		Label: "Select channel to remove",
		Items: items,
		Size:  min(10, len(items)),
	}

	index, _, err := prompt.Run()
	if err != nil {
		return "", fmt.Errorf("remove prompt failed: %w", err)
	}

	return watchedChannels[index].Name, nil
}

func findWatchedChannelIndex(watchedChannels []config.WatchedChannel, channelName string) int {
	for i, watchedChannel := range watchedChannels {
		if strings.EqualFold(watchedChannel.Name, channelName) {
			return i
		}
	}

	return -1
}

func removeWatchedChannel(watchedChannels *[]config.WatchedChannel, channelName string) (config.WatchedChannel, bool) {
	existingIndex := findWatchedChannelIndex(*watchedChannels, channelName)
	if existingIndex < 0 {
		return config.WatchedChannel{}, false
	}

	removedChannel := (*watchedChannels)[existingIndex]
	*watchedChannels = append((*watchedChannels)[:existingIndex], (*watchedChannels)[existingIndex+1:]...)
	return removedChannel, true
}

func upsertWatchedChannel(watchedChannels *[]config.WatchedChannel, channelName string, openValue bool) bool {
	channel := config.WatchedChannel{Name: channelName, Open: boolPtr(openValue)}
	existingIndex := findWatchedChannelIndex(*watchedChannels, channelName)
	if existingIndex >= 0 {
		(*watchedChannels)[existingIndex] = channel
		return true
	}

	*watchedChannels = append(*watchedChannels, channel)
	return false
}

func boolPtr(value bool) *bool {
	return &value
}

func formatWatchedChannelOption(watchedChannel config.WatchedChannel) string {
	openValue := false
	if watchedChannel.Open != nil {
		openValue = *watchedChannel.Open
	}

	return fmt.Sprintf("%s (open=%t)", watchedChannel.Name, openValue)
}

func min(a, b int) int {
	if a < b {
		return a
	}

	return b
}

func channelCommandUsage() string {
	return strings.TrimSpace(`Usage:
	  twitch-notifications channel add
	  twitch-notifications channel add <channel>
	  twitch-notifications channel add <channel> <true|false>
	  twitch-notifications channel add <true|false>
	  twitch-notifications channel remove
	  twitch-notifications channel remove <channel>

Missing values are prompted interactively.
Use -config /path/to/config.yaml to target a custom config.`)
}

func channelAddUsage() string {
	return strings.TrimSpace(`Usage:
	  twitch-notifications channel add
  twitch-notifications channel add <channel>
  twitch-notifications channel add <channel> <true|false>
  twitch-notifications channel add <true|false>

Examples:
  twitch-notifications channel add
	  twitch-notifications channel add pirateSoftware
	  twitch-notifications channel add pirateSoftware true
	  twitch-notifications channel add false`)
}

func channelRemoveUsage() string {
	return strings.TrimSpace(`Usage:
	  twitch-notifications channel remove
	  twitch-notifications channel remove <channel>

Examples:
	  twitch-notifications channel remove
	  twitch-notifications channel remove pirateSoftware`)
}
