package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"twitch-notifications/auth"
	"twitch-notifications/config"
	"twitch-notifications/notify"
	"twitch-notifications/tray"
	"twitch-notifications/twitch"
	"twitch-notifications/utils"

	"fyne.io/systray"
	"github.com/godbus/dbus/v5"
)

const (
	appName             = "Twitch Notifier"
	maxEventSubChannels = 10 // Twitch EventSub subscription cost limit

	// Startup timing
	systrayInitDelay         = 200 * time.Millisecond // Wait for systray to initialize
	subscriptionProcessDelay = 500 * time.Millisecond // Wait for subscriptions to process
	sessionEstablishTimeout  = 5 * time.Second        // Wait for EventSub session

	// Background task intervals
	healthCheckInterval  = 1 * time.Minute // Periodic health check (also refreshes tokens)
	periodicRestartDelay = 1 * time.Hour   // Periodic restart interval for long-running stability

	// Subscription timing
	baseSubscriptionDelay = 1 * time.Second // Delay between subscription requests
)

// restartRequested is set to true when the periodic restart timer fires,
// signaling main() to re-exec the process after cleanup.
var restartRequested atomic.Bool
var restartOpenRequested atomic.Bool

type barJSONStatusPayload struct {
	Text    string `json:"text"`
	Tooltip string `json:"tooltip"`
	Class   string `json:"class"`
}

type statusJSONChannel struct {
	Login        string `json:"login"`
	Title        string `json:"title"`
	ThumbnailURL string `json:"thumbnailUrl,omitempty"`
	Live         bool   `json:"live"`
	AutoOpen     bool   `json:"autoOpen"`
}

type statusJSONPayload struct {
	Active    bool                `json:"active"`
	State     string              `json:"state"`
	LiveCount int                 `json:"liveCount"`
	Channels  []statusJSONChannel `json:"channels"`
}

func printBarJSONStatus(text, tooltip, class string) {
	payload := barJSONStatusPayload{Text: text, Tooltip: tooltip, Class: class}
	encoded, err := json.Marshal(payload)
	if err != nil {
		fmt.Printf(`{"text":"%s","tooltip":"%s","class":"%s"}`+"\n", text, tooltip, class)
		return
	}

	fmt.Println(string(encoded))
}

func buildStatusJSONPayload(active bool, liveCount int, liveChannels []statusJSONChannel, watchedChannels []config.WatchedChannel) statusJSONPayload {
	state := "inactive"
	if active {
		state = "active"
		if liveCount > 0 {
			state = "live"
		}
	}

	liveByLogin := make(map[string]statusJSONChannel, len(liveChannels))
	for _, channel := range liveChannels {
		channel.Login = strings.TrimSpace(channel.Login)
		if channel.Login == "" {
			continue
		}
		channel.Live = true
		liveByLogin[strings.ToLower(channel.Login)] = channel
	}

	live := make([]statusJSONChannel, 0, len(liveByLogin))
	offline := make([]statusJSONChannel, 0, len(watchedChannels))
	for _, watched := range watchedChannels {
		login := strings.TrimSpace(watched.Name)
		if login == "" {
			continue
		}

		autoOpen := watched.Open != nil && *watched.Open
		key := strings.ToLower(login)
		if channel, ok := liveByLogin[key]; ok {
			channel.Login = login
			channel.AutoOpen = autoOpen
			live = append(live, channel)
			delete(liveByLogin, key)
			continue
		}

		offline = append(offline, statusJSONChannel{Login: login, AutoOpen: autoOpen})
	}

	// A live row should remain visible even if the running daemon and the
	// on-disk channel list briefly differ during an update.
	extraLive := make([]statusJSONChannel, 0, len(liveByLogin))
	for _, channel := range liveByLogin {
		extraLive = append(extraLive, channel)
	}
	sort.Slice(extraLive, func(i, j int) bool {
		return strings.ToLower(extraLive[i].Login) < strings.ToLower(extraLive[j].Login)
	})
	live = append(live, extraLive...)

	channels := append(live, offline...)
	return statusJSONPayload{
		Active:    active,
		State:     state,
		LiveCount: liveCount,
		Channels:  channels,
	}
}

func buildFollowedLiveChannels(streams []twitch.LiveStream, watchedChannels []config.WatchedChannel) []statusJSONChannel {
	watched := make(map[string]struct{}, len(watchedChannels))
	for _, channel := range watchedChannels {
		watched[strings.ToLower(strings.TrimSpace(channel.Name))] = struct{}{}
	}

	channels := make([]statusJSONChannel, 0, len(streams))
	for _, stream := range streams {
		if _, configured := watched[strings.ToLower(stream.BroadcasterUserLogin)]; configured {
			continue
		}
		channels = append(channels, statusJSONChannel{
			Login:        stream.BroadcasterUserLogin,
			Title:        stream.StreamTitle,
			ThumbnailURL: stream.ThumbnailURL,
			Live:         true,
		})
	}
	return channels
}

func truncateTooltipLine(value string, maxRunes int) string {
	if maxRunes <= 0 {
		return value
	}

	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}

	if maxRunes <= 3 {
		return string(runes[:maxRunes])
	}

	return string(runes[:maxRunes-3]) + "..."
}

func getDefaultConfigPath() string {
	usr, err := user.Current()
	if err != nil {
		// Fallback to current directory if we can't get user home
		return "config/config.yaml"
	}
	return filepath.Join(usr.HomeDir, ".config", "twitch-notifications", "config.yaml")
}

// createTokenPersistenceCallback creates a callback that persists tokens to the config file
// This is used to save tokens whenever they're refreshed by the TokenManager.
// Note: Tokens are kept in memory during runtime, so persistence failure only affects restarts.
func createTokenPersistenceCallback(configPath string) auth.TokenRefreshCallback {
	return func(accessToken, refreshToken string) {
		log.Println("Token refreshed, persisting to config file...")
		if err := config.SaveTokens(configPath, accessToken, refreshToken); err != nil {
			// Tokens still work in memory, but restart will need re-auth
			log.Printf("Warning: Failed to save tokens to config file: %v", err)
			log.Printf("Warning: If the application restarts, you will need to re-authenticate.")
		} else {
			log.Println("Tokens persisted successfully")
		}
	}
}

// setupTokenManager creates and configures a TokenManager with the given config
// It sets up the refresh token and persistence callback
func setupTokenManager(cfg *config.Config, configPath string) *auth.TokenManager {
	tokenManager := auth.NewTokenManager(
		cfg.Twitch.ClientID,
		cfg.Twitch.ClientSecret,
		cfg.Twitch.AccessToken,
	)

	if cfg.Twitch.RefreshToken != "" {
		tokenManager.SetRefreshToken(cfg.Twitch.RefreshToken)
	}

	tokenManager.SetOnTokenRefresh(createTokenPersistenceCallback(configPath))
	return tokenManager
}

// ChannelSplit contains the result of splitting channels between EventSub and polling
type ChannelSplit struct {
	EventSubChannels []twitch.Channel // Channels using real-time EventSub (max 10)
	PolledChannels   []twitch.Channel // Overflow channels using polling
}

// resolveWatchedChannels fetches channel info and splits them between EventSub and polling
// The first maxEventSubChannels channels get EventSub, the rest use polling
func resolveWatchedChannels(ctx context.Context, helixClient *twitch.HelixClient, cfg *config.Config) (*ChannelSplit, error) {
	if len(cfg.WatchedChannels) == 0 {
		return nil, nil // No channels configured
	}

	// Extract usernames from watched channels
	usernames := make([]string, 0, len(cfg.WatchedChannels))
	for _, wc := range cfg.WatchedChannels {
		usernames = append(usernames, wc.Name)
	}

	// Fetch channel IDs
	log.Printf("Looking up %d watched channels...", len(usernames))
	channels, err := helixClient.GetChannelsByUsernames(ctx, usernames)
	if err != nil {
		return nil, fmt.Errorf("failed to get channel information: %w", err)
	}

	if len(channels) == 0 {
		return nil, nil // No valid channels found
	}

	log.Printf("Found %d valid watched channels", len(channels))

	// Split channels into EventSub and polled
	split := &ChannelSplit{}
	if len(channels) <= maxEventSubChannels {
		split.EventSubChannels = channels
	} else {
		split.EventSubChannels = channels[:maxEventSubChannels]
		split.PolledChannels = channels[maxEventSubChannels:]
		log.Printf("Channel split: %d channels via EventSub (real-time), %d channels via polling",
			len(split.EventSubChannels), len(split.PolledChannels))
	}

	return split, nil
}

// sendRecheckCommand sends a recheck command to a running instance via DBus
// openBrowser: if true, the running instance will open browsers for live channels
func sendRecheckCommand(openBrowser bool) error {
	conn, err := dbus.SessionBus()
	if err != nil {
		return fmt.Errorf("failed to connect to session bus: %w", err)
	}
	defer conn.Close()

	obj := conn.Object("com.github.TwitchNotifications", "/com/github/TwitchNotifications")
	call := obj.Call("com.github.TwitchNotifications.Recheck", 0, openBrowser)
	if call.Err != nil {
		return fmt.Errorf("DBus call failed: %w", call.Err)
	}

	return nil
}

// sendRestartCommand asks a running instance to restart through its normal shutdown path.
func sendRestartCommand(openBrowser bool) error {
	conn, err := dbus.SessionBus()
	if err != nil {
		return fmt.Errorf("failed to connect to session bus: %w", err)
	}
	defer conn.Close()

	obj := conn.Object("com.github.TwitchNotifications", "/com/github/TwitchNotifications")
	method := "com.github.TwitchNotifications.Restart"
	if openBrowser {
		method = "com.github.TwitchNotifications.RestartOpen"
	}
	call := obj.Call(method, 0)
	if call.Err != nil {
		return fmt.Errorf("DBus call failed: %w", call.Err)
	}

	return nil
}

// getApplicationStatus checks if a running notifier instance is available via DBus,
// and returns whether it's active plus current live stream count.
func getApplicationStatus() (bool, int, []string, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return false, 0, nil, fmt.Errorf("failed to connect to session bus: %w", err)
	}
	defer conn.Close()

	obj := conn.Object("com.github.TwitchNotifications", "/com/github/TwitchNotifications")
	call := obj.Call("com.github.TwitchNotifications.GetStatus", 0)
	if call.Err != nil {
		if dbusErr, ok := call.Err.(*dbus.Error); ok {
			switch dbusErr.Name {
			case "org.freedesktop.DBus.Error.ServiceUnknown",
				"org.freedesktop.DBus.Error.UnknownObject":
				return false, 0, nil, nil
			case "org.freedesktop.DBus.Error.UnknownMethod":
				// Backward compatibility with older running instances.
				pingCall := obj.Call("com.github.TwitchNotifications.Ping", 0)
				if pingCall.Err != nil {
					if pingErr, ok := pingCall.Err.(*dbus.Error); ok {
						switch pingErr.Name {
						case "org.freedesktop.DBus.Error.ServiceUnknown",
							"org.freedesktop.DBus.Error.UnknownObject",
							"org.freedesktop.DBus.Error.UnknownMethod":
							return false, 0, nil, nil
						}
					}
					return false, 0, nil, fmt.Errorf("DBus call failed: %w", pingCall.Err)
				}

				var response string
				if err := pingCall.Store(&response); err != nil {
					return false, 0, nil, fmt.Errorf("failed to read DBus ping response: %w", err)
				}

				return response == "pong", 0, nil, nil
			}
		}
		return false, 0, nil, fmt.Errorf("DBus call failed: %w", call.Err)
	}

	var active bool
	var liveCount int32
	var liveChannels []string
	if err := call.Store(&active, &liveCount, &liveChannels); err != nil {
		return false, 0, nil, fmt.Errorf("failed to read DBus status response: %w", err)
	}

	if liveCount < 0 {
		liveCount = 0
	}

	return active, int(liveCount), liveChannels, nil
}

func getDetailedApplicationStatus() (bool, int, []statusJSONChannel, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return false, 0, nil, fmt.Errorf("failed to connect to session bus: %w", err)
	}
	defer conn.Close()

	obj := conn.Object("com.github.TwitchNotifications", "/com/github/TwitchNotifications")
	call := obj.Call("com.github.TwitchNotifications.GetDetailedStatus", 0)
	if call.Err != nil {
		if dbusErr, ok := call.Err.(*dbus.Error); ok && dbusErr.Name == "org.freedesktop.DBus.Error.UnknownMethod" {
			active, liveCount, legacyChannels, legacyErr := getApplicationStatus()
			channels := make([]statusJSONChannel, 0, len(legacyChannels))
			for _, value := range legacyChannels {
				login, title, _ := strings.Cut(value, ": ")
				channels = append(channels, statusJSONChannel{
					Login: strings.TrimSpace(login),
					Title: strings.TrimSpace(title),
					Live:  true,
				})
			}
			return active, liveCount, channels, legacyErr
		}
		return false, 0, nil, fmt.Errorf("DBus call failed: %w", call.Err)
	}

	var active bool
	var liveCount int32
	var liveChannelsJSON string
	if err := call.Store(&active, &liveCount, &liveChannelsJSON); err != nil {
		return false, 0, nil, fmt.Errorf("failed to read detailed DBus status response: %w", err)
	}

	var liveChannels []statusJSONChannel
	if err := json.Unmarshal([]byte(liveChannelsJSON), &liveChannels); err != nil {
		return false, 0, nil, fmt.Errorf("failed to decode detailed DBus status response: %w", err)
	}
	if liveCount < 0 {
		liveCount = 0
	}

	return active, int(liveCount), liveChannels, nil
}

func main() {
	logCloser, err := utils.SetupLogging("twitch-notifications")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to initialize file logging: %v\n", err)
	} else {
		defer logCloser.Close()
	}

	// Top-level panic recovery: if main itself panics, restart silently
	defer func() {
		if r := recover(); r != nil {
			log.Printf("FATAL PANIC in main: %v — restarting...", r)
			utils.RestartSelf(false)
		}
	}()

	defaultConfigPath := getDefaultConfigPath()
	if handled, err := handleCLICommand(os.Args[1:], defaultConfigPath); handled {
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		return
	}

	// If no arguments were given and we're in an interactive terminal,
	// try to launch the TUI instead of the server.
	if len(os.Args) == 1 {
		maybeLaunchTUI()
	}

	configPath := flag.String("config", defaultConfigPath, "Path to configuration file")
	recheck := flag.Bool("recheck", false, "Trigger a recheck for live channels on a running instance and exit")
	restart := flag.Bool("restart", false, "Gracefully restart a running instance and exit")
	openBrowser := flag.Bool("open", false, "Open configured live channels during a recheck or restarted instance's initial check")
	status := flag.Bool("status", false, "Print whether the application is active (active|inactive) and exit")
	statusBarJSON := flag.Bool("status-bar-json", false, "Print status bar JSON status and exit")
	statusJSON := flag.Bool("status-json", false, "Print structured daemon and channel status as JSON and exit")
	followedLiveJSON := flag.Bool("followed-live-json", false, "Print unconfigured followed channels that are live as JSON and exit")
	maxChars := flag.Int("max-chars", 0, "Maximum characters per live channel line in --status-bar-json tooltip")
	startupDelay := flag.Bool("delay", false, "Delay startup to allow the previous instance to fully shut down (used during periodic restart)")
	silent := flag.Bool("silent", false, "Suppress startup and initial monitoring notifications (used during periodic restart)")
	flag.Parse()
	if *followedLiveJSON {
		cfg, err := config.Load(*configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
			os.Exit(1)
		}

		tokenManager := setupTokenManager(cfg, *configPath)
		accessToken, err := tokenManager.GetAccessToken(context.Background())
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to get access token: %v\n", err)
			os.Exit(1)
		}
		helixClient, err := twitch.NewHelixClient(cfg.Twitch.ClientID, accessToken)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create Twitch client: %v\n", err)
			os.Exit(1)
		}
		userID, err := helixClient.GetUserID(context.Background())
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to get Twitch user: %v\n", err)
			os.Exit(1)
		}
		streams, err := helixClient.GetFollowedLiveStreams(context.Background(), userID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to get followed live streams: %v\n", err)
			os.Exit(1)
		}
		if err := json.NewEncoder(os.Stdout).Encode(buildFollowedLiveChannels(streams, cfg.WatchedChannels)); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to encode followed live streams: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	// Handle status checks: query DBus service and exit.
	if *status || *statusBarJSON || *statusJSON {
		if *statusJSON {
			active, liveCount, liveChannels, err := getDetailedApplicationStatus()
			if err != nil {
				active = false
				liveCount = 0
				liveChannels = nil
			}

			cfg, configErr := config.Load(*configPath)
			if configErr != nil {
				fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", configErr)
				os.Exit(1)
			}

			if encodeErr := json.NewEncoder(os.Stdout).Encode(buildStatusJSONPayload(active, liveCount, liveChannels, cfg.WatchedChannels)); encodeErr != nil {
				fmt.Fprintf(os.Stderr, "Failed to encode status: %v\n", encodeErr)
				os.Exit(1)
			}
			os.Exit(0)
		}

		active, liveCount, liveChannels, err := getApplicationStatus()
		if *statusBarJSON {
			if err != nil || !active {
				printBarJSONStatus("󰂚", "Twitch Notifications is inactive", "inactive")
			} else if liveCount > 0 {
				if *maxChars > 0 {
					for i, liveChannel := range liveChannels {
						liveChannels[i] = truncateTooltipLine(liveChannel, *maxChars)
					}
				}
				sort.Strings(liveChannels)
				tooltip := "Live channels:\n- " + strings.Join(liveChannels, "\n- ")
				printBarJSONStatus(fmt.Sprintf("󰂚 %d", liveCount), tooltip, "live")
			} else {
				printBarJSONStatus("󰂜 0", "Twitch Notifications is active", "active")
			}
			os.Exit(0)
		}

		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to check application status: %v\n", err)
			os.Exit(1)
		}

		if active {
			fmt.Println("active")
		} else {
			fmt.Println("inactive")
		}
		os.Exit(0)
	}

	// If -delay is set, wait for the previous instance to release resources.
	// The delay grows on consecutive rapid restarts to avoid a tight crash loop.
	if *startupDelay {
		delay := utils.RestartDelay()
		log.Printf("Startup delay: waiting %v for previous instance to shut down...", delay)
		time.Sleep(delay)
		log.Println("Startup delay complete, continuing initialization...")
	}

	// Mark the point where real work begins. If the process runs stably past the
	// stability window before any restart, the rapid-restart backoff resets.
	utils.MarkWorkStarted()

	// Handle --recheck: send DBus message to running instance and exit
	if *recheck {
		if err := sendRecheckCommand(*openBrowser); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to send recheck command: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Recheck command sent successfully")
		os.Exit(0)
	}

	if *restart {
		if err := sendRestartCommand(*openBrowser); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to send restart command: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Restart command sent successfully")
		os.Exit(0)
	}

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		utils.FatalError("Failed to load config: %v", err)
	}

	// Validate configuration
	if cfg.Twitch.ClientID == "" {
		utils.FatalError("Twitch Client ID is required")
	}
	if cfg.Twitch.ClientSecret == "" {
		utils.FatalError("Twitch Client Secret is required")
	}

	// Initialize components
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Create notifier early for fatal error notifications
	// The openURL function is used when a notification is clicked
	browserOpenerEarly := utils.NewOpener()
	earlyNotifier, err := notify.NewNotifier(appName, cfg.SoundFile, browserOpenerEarly.OpenURL)
	if err != nil {
		log.Printf("Warning: Failed to create early notifier: %v", err)
	} else {
		utils.SetGlobalNotifier(earlyNotifier)
	}

	// Start the system tray in a goroutine (config path is set during config.Load())
	// On Linux, systray uses GTK which needs the main thread available
	go systray.Run(tray.OnReady, tray.OnExit)

	// Give systray a moment to initialize before starting heavy work
	time.Sleep(systrayInitDelay)

	// Run all initialization in a goroutine so it doesn't block the main thread
	// This is important on Linux where systray needs the main thread for GTK
	initError := make(chan error, 1)
	var eventSubClient *twitch.EventSubClient

	// Channel to signal that the application should restart due to a crash
	crashRestart := make(chan struct{}, 1)

	// Start notifier in background - it will run until context is cancelled
	go func() {
		// Recover from panics in runNotifier and trigger a restart
		defer func() {
			if r := recover(); r != nil {
				log.Printf("FATAL PANIC in runNotifier: %v — scheduling restart...", r)
				select {
				case crashRestart <- struct{}{}:
				default:
				}
			}
		}()

		if err := runNotifier(ctx, cfg, *configPath, *silent, *openBrowser, &eventSubClient); err != nil {
			select {
			case initError <- err:
			default:
			}
		}
	}()

	// Wait for shutdown signal, initialization error, or crash restart
	select {
	case sig := <-sigChan:
		log.Printf("Received signal: %v, shutting down...", sig)
	case err := <-initError:
		log.Printf("Initialization failed: %v — restarting...", err)
		// Initialization failures may be transient (network issues, API errors, etc.)
		// so restart silently instead of exiting fatally
		restartRequested.Store(true)
	case <-crashRestart:
		log.Println("Crash detected, restarting...")
		restartRequested.Store(true)
	case <-ctx.Done():
		log.Println("Shutting down...")
	}

	// Cancel context to stop all goroutines
	cancel()

	// Close EventSub client (this will close WebSocket and wait for goroutines)
	if eventSubClient != nil {
		if err := eventSubClient.Close(); err != nil {
			log.Printf("Error closing EventSub client: %v", err)
		}
	}

	// Close global notifier
	utils.CloseGlobalNotifier()

	// Close DBus service
	utils.CloseDBusService()

	// Quit system tray
	systray.Quit()

	// If periodic restart or crash restart was requested, spawn a new instance before exiting
	if restartRequested.Load() {
		log.Println("Restarting...")
		utils.RestartSelf(restartOpenRequested.Load())
	}

	log.Println("Shutdown complete")
}

// isAuthError checks if an error indicates an authentication failure (401 status code)
// It handles both typed APIError from the twitch package and string-based errors from auth package
func isAuthError(err error) bool {
	if err == nil {
		return false
	}

	// Check for typed APIError first (preferred)
	var apiErr *twitch.APIError
	if errors.As(err, &apiErr) {
		return apiErr.IsAuthError()
	}

	// Fallback for auth package errors that use string formatting
	// This handles "token refresh failed (status 401)" pattern from RefreshAccessToken
	errStr := err.Error()
	if strings.Contains(errStr, "token refresh failed (status 401)") {
		return true
	}

	return false
}

// AuthRetryResult contains the result of a successful OAuth retry
type AuthRetryResult struct {
	Config       *config.Config
	TokenManager *auth.TokenManager
	HelixClient  *twitch.HelixClient
	AccessToken  string
}

// LiveStatusTracker keeps track of channels currently known to be live.
type LiveStatusTracker struct {
	mu             sync.RWMutex
	liveChannelIDs map[string]bool
}

// NewLiveStatusTracker creates a new live status tracker.
func NewLiveStatusTracker() *LiveStatusTracker {
	return &LiveStatusTracker{liveChannelIDs: make(map[string]bool)}
}

// Snapshot returns a copy of the current live channel ID set.
func (t *LiveStatusTracker) Snapshot() map[string]bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	snapshot := make(map[string]bool, len(t.liveChannelIDs))
	for channelID, isLive := range t.liveChannelIDs {
		if isLive {
			snapshot[channelID] = true
		}
	}

	return snapshot
}

// Replace sets the current live channel ID set.
func (t *LiveStatusTracker) Replace(liveChannelIDs map[string]bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.liveChannelIDs = make(map[string]bool, len(liveChannelIDs))
	for channelID, isLive := range liveChannelIDs {
		if isLive {
			t.liveChannelIDs[channelID] = true
		}
	}
}

// MarkLive marks a channel as live.
func (t *LiveStatusTracker) MarkLive(channelID string) {
	if channelID == "" {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.liveChannelIDs[channelID] = true
}

// Count returns the number of currently live channels.
func (t *LiveStatusTracker) Count() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.liveChannelIDs)
}

// Application holds the shared mutable state for the notifier application.
// All mutable fields are protected by mu to ensure thread-safety when accessed
// from multiple goroutines (health check, token refresh, etc.).
type Application struct {
	mu sync.RWMutex

	// Mutable state (protected by mu)
	cfg          *config.Config
	tokenManager *auth.TokenManager
	helixClient  *twitch.HelixClient

	// Immutable state (set once during init, no lock needed for reads)
	configPath    string
	notifier      *notify.Notifier
	browserOpener *utils.Opener
}

// NewApplication creates a new Application with the given initial state
func NewApplication(cfg *config.Config, configPath string, tokenManager *auth.TokenManager,
	helixClient *twitch.HelixClient, notifier *notify.Notifier, browserOpener *utils.Opener,
) *Application {
	return &Application{
		cfg:           cfg,
		configPath:    configPath,
		tokenManager:  tokenManager,
		helixClient:   helixClient,
		notifier:      notifier,
		browserOpener: browserOpener,
	}
}

// Config returns the current config (thread-safe)
func (app *Application) Config() *config.Config {
	app.mu.RLock()
	defer app.mu.RUnlock()
	return app.cfg
}

// TokenManager returns the current token manager (thread-safe)
func (app *Application) TokenManager() *auth.TokenManager {
	app.mu.RLock()
	defer app.mu.RUnlock()
	return app.tokenManager
}

// HelixClient returns the current Helix client (thread-safe)
func (app *Application) HelixClient() *twitch.HelixClient {
	app.mu.RLock()
	defer app.mu.RUnlock()
	return app.helixClient
}

// UpdateFromAuthRetry updates the application state after a successful OAuth retry
func (app *Application) UpdateFromAuthRetry(result *AuthRetryResult) {
	app.mu.Lock()
	defer app.mu.Unlock()
	app.cfg = result.Config
	app.tokenManager = result.TokenManager
	app.helixClient = result.HelixClient
}

// UpdateHelixClientToken updates the Helix client's access token (thread-safe)
func (app *Application) UpdateHelixClientToken(token string) {
	app.mu.Lock()
	defer app.mu.Unlock()
	app.helixClient.UpdateAccessToken(token)
}

// shouldTriggerOAuthRetry determines if an error warrants OAuth retry
// It checks for typed auth errors and specific error messages that indicate auth failure
func shouldTriggerOAuthRetry(err error) bool {
	if isAuthError(err) {
		return true
	}

	errStr := err.Error()
	// "no valid access token and no refresh token available" means we need fresh OAuth
	if strings.Contains(errStr, "no valid access token") {
		return true
	}
	// "failed to refresh token" with auth-related details
	if strings.Contains(errStr, "failed to refresh token") && isAuthError(err) {
		return true
	}

	return false
}

// handleAuthErrorWithRetry attempts OAuth retry if the error warrants it.
// Returns (nil, nil) if the error doesn't warrant OAuth retry (caller should handle original error).
// Returns (result, nil) on successful retry.
// Returns (nil, error) if OAuth retry fails.
func handleAuthErrorWithRetry(ctx context.Context, configPath string, notifier *notify.Notifier, cfg *config.Config, originalErr error) (*AuthRetryResult, error) {
	if !shouldTriggerOAuthRetry(originalErr) {
		return nil, nil
	}

	log.Printf("Auth error detected, triggering OAuth flow: %v", originalErr)

	newCfg, tokenManager, accessToken, err := retryOAuthFlow(ctx, configPath, notifier)
	if err != nil {
		return nil, fmt.Errorf("OAuth retry failed: %w", err)
	}

	helixClient, err := twitch.NewHelixClient(newCfg.Twitch.ClientID, accessToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create Helix client after OAuth: %w", err)
	}

	return &AuthRetryResult{
		Config:       newCfg,
		TokenManager: tokenManager,
		HelixClient:  helixClient,
		AccessToken:  accessToken,
	}, nil
}

// runNotifier runs the notifier initialization and main loop in a goroutine
// silentStartup suppresses startup and initial monitoring notifications (e.g. after periodic restart)
func runNotifier(ctx context.Context, cfg *config.Config, configPath string, silentStartup bool, openOnStartup bool, eventSubClient **twitch.EventSubClient) error {
	// Initialize notifier early for auth notifications
	var notifier *notify.Notifier
	var err error

	browserOpener := utils.NewOpener()
	notifier, err = notify.NewNotifier(appName, cfg.SoundFile, browserOpener.OpenURL)
	if err != nil {
		return fmt.Errorf("failed to create notifier: %w", err)
	}
	// Don't defer close here - the notifier is stored globally and should remain open
	// for the lifetime of the application. It will be closed during shutdown.

	// Update global notifier to use the fully configured one
	// Close any existing early notifier first to prevent resource leak
	utils.CloseAndSetGlobalNotifier(notifier)

	// Initialize token manager with persistence callback
	tokenManager := setupTokenManager(cfg, configPath)

	// Get access token (will attempt refresh if access token is empty but refresh token exists)
	accessToken, err := tokenManager.GetAccessToken(ctx)
	if err != nil {
		log.Printf("Failed to get access token: %v", err)

		// Try OAuth retry if it's an auth error or missing token
		result, retryErr := handleAuthErrorWithRetry(ctx, configPath, notifier, cfg, err)
		if retryErr != nil {
			return retryErr
		}
		if result == nil {
			// Not an auth error, return original error
			return fmt.Errorf("failed to get access token: %w", err)
		}
		cfg, tokenManager, accessToken = result.Config, result.TokenManager, result.AccessToken
	}

	// Initialize Helix client
	helixClient, err := twitch.NewHelixClient(cfg.Twitch.ClientID, accessToken)
	if err != nil {
		return fmt.Errorf("failed to create Helix client: %w", err)
	}

	// Get user ID
	userID, err := helixClient.GetUserID(ctx)
	if err != nil {
		// Try OAuth retry if it's an auth error
		result, retryErr := handleAuthErrorWithRetry(ctx, configPath, notifier, cfg, err)
		if retryErr != nil {
			return retryErr
		}
		if result == nil {
			// Not an auth error, return original error
			return fmt.Errorf("failed to get user ID: %w", err)
		}
		// Update state from successful retry
		cfg, tokenManager, helixClient, accessToken = result.Config, result.TokenManager, result.HelixClient, result.AccessToken

		// Retry getting user ID with new client
		userID, err = helixClient.GetUserID(ctx)
		if err != nil {
			return fmt.Errorf("failed to get user ID after OAuth setup: %w", err)
		}
	}

	log.Printf("Authenticated as user ID: %s", userID)

	// Resolve watched channels and split between EventSub and polling
	channelSplit, err := resolveWatchedChannels(ctx, helixClient, cfg)
	if err != nil {
		return err
	}
	if channelSplit == nil {
		log.Println("No watched_channels configured or no valid channels found. Exiting.")
		return nil
	}
	eventSubChannels := channelSplit.EventSubChannels
	polledChannels := channelSplit.PolledChannels

	// Create application state holder for thread-safe access to mutable state
	app := NewApplication(cfg, configPath, tokenManager, helixClient, notifier, browserOpener)
	liveStatus := NewLiveStatusTracker()

	eventSubChannelIDSet := make(map[string]bool, len(eventSubChannels))
	eventSubChannelIDs := make([]string, 0, len(eventSubChannels))
	for _, channel := range eventSubChannels {
		eventSubChannelIDSet[channel.ID] = true
		eventSubChannelIDs = append(eventSubChannelIDs, channel.ID)
	}

	polledChannelIDSet := make(map[string]bool, len(polledChannels))
	for _, channel := range polledChannels {
		polledChannelIDSet[channel.ID] = true
	}

	channelNameByID := make(map[string]string, len(eventSubChannels)+len(polledChannels))
	for _, channel := range eventSubChannels {
		channelNameByID[channel.ID] = channel.Username
	}
	for _, channel := range polledChannels {
		channelNameByID[channel.ID] = channel.Username
	}

	liveStreamByChannelID := make(map[string]twitch.LiveStream)
	var liveStreamMu sync.RWMutex

	// Setup stream online handler (shared between EventSub and Poller)
	onStreamOnline := func(event twitch.StreamOnlineEvent) {
		liveStatus.MarkLive(event.BroadcasterUserID)

		if event.BroadcasterUserID != "" && (event.StreamTitle == "" || event.GameName == "" || event.ThumbnailURL == "") {
			liveStreams, err := app.HelixClient().GetLiveStreams(ctx, []string{event.BroadcasterUserID})
			if err != nil {
				log.Printf("Failed to hydrate live stream metadata for %s (%s): %v", event.BroadcasterUserName, event.BroadcasterUserLogin, err)
			} else if liveStream, ok := liveStreams[event.BroadcasterUserID]; ok {
				if event.StreamTitle == "" {
					event.StreamTitle = liveStream.StreamTitle
				}
				if event.GameName == "" {
					event.GameName = liveStream.GameName
				}
				if event.ThumbnailURL == "" {
					event.ThumbnailURL = liveStream.ThumbnailURL
				}
			}
		}

		if event.BroadcasterUserID != "" {
			liveStreamMu.Lock()
			liveStreamByChannelID[event.BroadcasterUserID] = twitch.LiveStream{
				BroadcasterUserID:    event.BroadcasterUserID,
				BroadcasterUserLogin: event.BroadcasterUserLogin,
				BroadcasterUserName:  event.BroadcasterUserName,
				StreamTitle:          event.StreamTitle,
				GameName:             event.GameName,
				ThumbnailURL:         event.ThumbnailURL,
				StartedAt:            event.StartedAt,
			}
			liveStreamMu.Unlock()
		}

		log.Printf("Stream online: %s (%s) - %s", event.BroadcasterUserName, event.BroadcasterUserLogin, event.StreamTitle)

		stream := StreamInfo{
			UserName:  event.BroadcasterUserName,
			UserLogin: event.BroadcasterUserLogin,
			Title:     event.StreamTitle,
			GameName:  event.GameName,
		}
		// For real-time events, always auto-open based on config
		opts := RecheckOptions{
			OpenBrowser:   true,
			BrowserOpener: app.browserOpener,
			Config:        app.Config(),
		}
		notifyAndOpenStream(app.notifier, stream, opts)
	}

	// Initialize poller for overflow channels (channels beyond EventSub limit)
	var poller *twitch.Poller
	if len(polledChannels) > 0 {
		pollInterval := time.Duration(app.Config().GetPollInterval()) * time.Second
		poller = twitch.NewPoller(app.HelixClient(), polledChannels, pollInterval, onStreamOnline)
	}

	refreshLiveStatus := func() {
		nextLive := liveStatus.Snapshot()

		liveStreamMu.RLock()
		nextLiveStreams := make(map[string]twitch.LiveStream, len(nextLive))
		for channelID := range nextLive {
			if stream, ok := liveStreamByChannelID[channelID]; ok {
				nextLiveStreams[channelID] = stream
			}
		}
		liveStreamMu.RUnlock()

		if len(eventSubChannelIDs) > 0 {
			liveStreams, err := app.HelixClient().GetLiveStreams(ctx, eventSubChannelIDs)
			if err != nil {
				log.Printf("Failed to refresh eventsub live status: %v", err)
			} else {
				for channelID := range eventSubChannelIDSet {
					delete(nextLive, channelID)
					delete(nextLiveStreams, channelID)
				}
				for channelID, liveStream := range liveStreams {
					nextLive[channelID] = true
					nextLiveStreams[channelID] = liveStream
				}
			}
		}

		if poller != nil {
			for channelID := range polledChannelIDSet {
				delete(nextLive, channelID)
				delete(nextLiveStreams, channelID)
			}

			polledLiveIDs := poller.GetLiveChannelIDs()
			for _, channelID := range polledLiveIDs {
				nextLive[channelID] = true
			}

			if len(polledLiveIDs) > 0 {
				polledLiveStreams, err := app.HelixClient().GetLiveStreams(ctx, polledLiveIDs)
				if err != nil {
					log.Printf("Failed to refresh polled live stream titles: %v", err)
				} else {
					for channelID, liveStream := range polledLiveStreams {
						nextLiveStreams[channelID] = liveStream
					}
				}
			}
		}

		liveStatus.Replace(nextLive)

		liveStreamMu.Lock()
		liveStreamByChannelID = nextLiveStreams
		liveStreamMu.Unlock()
	}

	// Track if we've done initial subscription
	var initialSubscriptionDone bool
	var initialSubMu sync.Mutex

	// Track if we've done initial live stream check (only on first startup)
	var initialLiveCheckDone bool
	var initialLiveCheckMu sync.Mutex

	// Setup session ready handler for resubscriptions
	onSessionReady := func(sessionID string) {
		initialSubMu.Lock()
		isInitial := !initialSubscriptionDone
		initialSubMu.Unlock()

		if isInitial {
			// First time - do initial subscription (only for EventSub channels, not polled ones)
			log.Printf("EventSub session ready, subscribing to %d channels...", len(eventSubChannels))
			initialSubMu.Lock()
			initialSubscriptionDone = true
			initialSubMu.Unlock()
			subscribeWithRateLimit(ctx, helixClient, *eventSubClient, sessionID, nil, eventSubChannels, false)

			// Start the poller for overflow channels after EventSub setup
			if poller != nil {
				poller.Start(ctx)
			}

			// After initial subscription, check for channels that are already live (only on first startup)
			initialLiveCheckMu.Lock()
			shouldCheckLive := !initialLiveCheckDone
			if shouldCheckLive {
				initialLiveCheckDone = true
			}
			initialLiveCheckMu.Unlock()

			if shouldCheckLive {
				// Use a goroutine with a small delay to ensure all subscriptions are marked
				utils.GoWithRecovery("initial-live-check", func() {
					time.Sleep(subscriptionProcessDelay) // Brief delay to ensure subscriptions are fully processed

					opts := RecheckOptions{
						OpenBrowser:       openOnStartup,
						BrowserOpener:     app.browserOpener,
						Config:            app.Config(),
						NotifyIfNoStreams: false,         // Handle "no streams" after checking all channel types
						Silent:            silentStartup, // Suppress all notifications during silent restart
					}

					eventSubLive := checkAndNotifyLiveStreams(ctx, helixClient, eventSubChannels, notifier, opts)

					polledLive := false
					if poller != nil {
						polledLive = checkAndNotifyPolledStreams(ctx, poller, notifier, opts)
					}

					if !silentStartup && !eventSubLive && !polledLive {
						if err := notifier.NotifyNoStreamsLive(); err != nil {
							log.Printf("Failed to send no streams live notification: %v", err)
						}
					}

					refreshLiveStatus()
					tray.RefreshStatus()
				})
			}
		} else {
			// Reconnection - resubscribe to existing subscriptions
			log.Printf("EventSub session ready, resubscribing to channels...")
			subscribedChannels := (*eventSubClient).GetSubscribedChannels()
			if len(subscribedChannels) > 0 {
				subscribeWithRateLimit(ctx, helixClient, *eventSubClient, sessionID, subscribedChannels, eventSubChannels, true)
			}
		}
	}

	// Initialize EventSub client
	*eventSubClient = twitch.NewEventSubClient(cfg.Twitch.ClientID, accessToken, onStreamOnline, onSessionReady)

	// Connect to EventSub
	log.Println("Connecting to Twitch EventSub...")
	if err := (*eventSubClient).Connect(); err != nil {
		return fmt.Errorf("failed to connect to EventSub: %w", err)
	}

	// Wait for session to be established and initial subscription to complete
	// The onSessionReady callback will handle the initial subscription
	time.Sleep(sessionEstablishTimeout)

	// Check if we got a session ID (onSessionReady should have been called)
	sessionID := (*eventSubClient).GetSessionID()
	if sessionID == "" {
		return fmt.Errorf("failed to get EventSub session ID")
	}

	log.Println("Twitch Notifier is running. Press Ctrl+C to stop.")

	// Send startup notification if enabled and not in silent mode
	if app.Config().NotifyOnStartup && !silentStartup {
		if err := app.notifier.NotifyStartup(true); err != nil {
			log.Printf("Failed to send startup notification: %v", err)
		}
	}

	// Create recheck handler function (shared between tray menu and DBus)
	// openBrowser: if true, opens browser for live channels based on config settings
	recheckHandler := func(openBrowser bool) {
		opts := RecheckOptions{
			OpenBrowser:       openBrowser,
			BrowserOpener:     app.browserOpener,
			Config:            app.Config(),
			NotifyIfNoStreams: false, // We'll handle notification after checking all channels
		}
		// Check EventSub channels
		eventSubLive := checkAndNotifyLiveStreams(ctx, app.HelixClient(), eventSubChannels, app.notifier, opts)

		// Also check polled channels if poller is active
		polledLive := false
		if poller != nil {
			polledLive = checkAndNotifyPolledStreams(ctx, poller, app.notifier, opts)
		}

		// Send single notification if no streams are live across all channels
		if !eventSubLive && !polledLive {
			if err := app.notifier.NotifyNoStreamsLive(); err != nil {
				log.Printf("Failed to send no streams live notification: %v", err)
			}
		}

		refreshLiveStatus()
		tray.RefreshStatus()
	}

	// Register recheck handler for tray menu (does not open browser)
	// This allows users to manually trigger a check for live channels
	tray.SetRecheckHandler(func() {
		log.Println("Manual recheck triggered from tray menu")
		recheckHandler(false)
	})

	// Register recheck-and-open handler for tray menu (opens browser for live channels)
	tray.SetRecheckOpenHandler(func() {
		log.Println("Manual recheck (with open) triggered from tray menu")
		recheckHandler(true)
	})

	// Register restart handler for tray menu
	tray.SetRestartHandler(func() {
		log.Println("Restart triggered from tray menu")
		restartRequested.Store(true)
		if err := utils.SendShutdownSignal(); err != nil {
			log.Printf("Failed to send shutdown signal for restart: %v", err)
		}
	})

	// Register status handler for tray menu (live channel list with logins for click-to-open)
	tray.SetStatusHandler(func() []tray.LiveChannel {
		snapshot := liveStatus.Snapshot()

		liveStreamMu.RLock()
		streamByChannelID := make(map[string]twitch.LiveStream, len(liveStreamByChannelID))
		for channelID, stream := range liveStreamByChannelID {
			streamByChannelID[channelID] = stream
		}
		liveStreamMu.RUnlock()

		channels := make([]tray.LiveChannel, 0, len(snapshot))
		for channelID := range snapshot {
			login := channelNameByID[channelID]
			if login == "" {
				login = channelID
			}

			displayName := login
			streamTitle := strings.TrimSpace(strings.ReplaceAll(streamByChannelID[channelID].StreamTitle, "\n", " "))
			if streamTitle != "" {
				displayName = fmt.Sprintf("%s: %s", login, streamTitle)
			}

			channels = append(channels, tray.LiveChannel{
				DisplayName: displayName,
				Login:       login,
			})
		}

		sort.Slice(channels, func(i, j int) bool {
			return channels[i].DisplayName < channels[j].DisplayName
		})
		return channels
	})

	// Register open-stream handler for tray menu (opens a Twitch stream by login)
	tray.SetOpenStreamHandler(func(login string) {
		url := utils.TwitchStreamURL(login)
		log.Printf("Opening Twitch stream from tray: %s", url)
		app.browserOpener.OpenURL(url)
	})

	// Register log path handler for tray menu
	tray.SetLogPathHandler(func() string {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			log.Printf("Failed to get home directory for log path: %v", err)
			return ""
		}
		logFile := time.Now().Format("2006-01-02") + ".log"
		return filepath.Join(homeDir, ".local", "state", "twitch-notifications", logFile)
	})

	// Register TUI launcher for tray menu (opens TUI in a terminal emulator)
	tray.SetLaunchTUIHandler(func() {
		tuiPath, found := findTUIBinary()
		if !found {
			log.Printf("Cannot launch TUI: %s binary not found", tuiBinaryName)
			return
		}
		launchTUIInTerminal(tuiPath)
	})

	// Register DBus service for IPC (allows triggering recheck via dbus-send)
	// This enables compositor hotkey bindings on Wayland
	dbusService, err := utils.NewDBusService()
	if err != nil {
		log.Printf("Warning: Failed to register DBus service (non-fatal): %v", err)
	} else {
		dbusService.SetRecheckHandler(recheckHandler)
		dbusService.SetRestartHandler(func(openBrowser bool) {
			restartOpenRequested.Store(openBrowser)
			restartRequested.Store(true)
			if err := utils.SendShutdownSignal(); err != nil {
				log.Printf("Failed to send shutdown signal for restart: %v", err)
			}
		})
		dbusService.SetStatusHandler(func() (int, []string) {
			snapshot := liveStatus.Snapshot()

			liveStreamMu.RLock()
			streamByChannelID := make(map[string]twitch.LiveStream, len(liveStreamByChannelID))
			for channelID, stream := range liveStreamByChannelID {
				streamByChannelID[channelID] = stream
			}
			liveStreamMu.RUnlock()

			liveChannels := make([]string, 0, len(snapshot))
			for channelID := range snapshot {
				channelName := channelNameByID[channelID]
				if channelName == "" {
					channelName = channelID
				}

				streamTitle := strings.TrimSpace(strings.ReplaceAll(streamByChannelID[channelID].StreamTitle, "\n", " "))
				if streamTitle != "" {
					liveChannels = append(liveChannels, fmt.Sprintf("%s: %s", channelName, streamTitle))
				} else {
					liveChannels = append(liveChannels, channelName)
				}
			}

			sort.Strings(liveChannels)
			return len(liveChannels), liveChannels
		})
		dbusService.SetDetailedStatusHandler(func() (int, string) {
			snapshot := liveStatus.Snapshot()

			liveStreamMu.RLock()
			streamByChannelID := make(map[string]twitch.LiveStream, len(liveStreamByChannelID))
			for channelID, stream := range liveStreamByChannelID {
				streamByChannelID[channelID] = stream
			}
			liveStreamMu.RUnlock()

			channels := make([]statusJSONChannel, 0, len(snapshot))
			for channelID := range snapshot {
				stream := streamByChannelID[channelID]
				login := channelNameByID[channelID]
				if login == "" {
					login = stream.BroadcasterUserLogin
				}
				if login == "" {
					login = channelID
				}
				channels = append(channels, statusJSONChannel{
					Login:        login,
					Title:        strings.TrimSpace(strings.ReplaceAll(stream.StreamTitle, "\n", " ")),
					ThumbnailURL: stream.ThumbnailURL,
					Live:         true,
				})
			}
			sort.Slice(channels, func(i, j int) bool {
				return strings.ToLower(channels[i].Login) < strings.ToLower(channels[j].Login)
			})

			encoded, err := json.Marshal(channels)
			if err != nil {
				log.Printf("Failed to encode detailed status: %v", err)
				return len(channels), "[]"
			}
			return len(channels), string(encoded)
		})
	}

	refreshLiveStatus()
	tray.RefreshStatus()

	utils.GoWithRecovery("live-status-refresh", func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				refreshLiveStatus()
				tray.RefreshStatus()
			}
		}
	})

	// Health check function called periodically to keep the connection and token fresh.
	runHealthCheck := func() {
		log.Println("Running health check...")

		// Check if EventSub connection is still alive
		sessionID := (*eventSubClient).GetSessionID()
		connectionHealthy := sessionID != ""
		if !connectionHealthy {
			log.Printf("Warning: EventSub session ID is empty, connection may be dead")
			// The connection should auto-reconnect, but we can log it
		} else {
			log.Printf("Health check: EventSub session active (ID: %s)", sessionID)
		}

		// First, try to get a valid token (this may trigger a refresh)
		newToken, err := app.TokenManager().GetAccessToken(ctx)
		if err != nil {
			log.Printf("Health check: Failed to get access token: %v", err)

			// Try OAuth retry if warranted
			result, retryErr := handleAuthErrorWithRetry(ctx, app.configPath, app.notifier, app.Config(), err)
			if retryErr != nil {
				log.Printf("Health check: OAuth retry failed: %v", retryErr)
			} else if result != nil {
				app.UpdateFromAuthRetry(result)
				(*eventSubClient).UpdateAccessToken(result.AccessToken)
			}
		} else {
			// Token obtained, update clients
			app.UpdateHelixClientToken(newToken)
			(*eventSubClient).UpdateAccessToken(newToken)

			// Make actual API call to verify token works
			_, apiErr := app.HelixClient().GetUserID(ctx)
			if apiErr != nil {
				log.Printf("Health check: API call failed: %v", apiErr)
				result, retryErr := handleAuthErrorWithRetry(ctx, app.configPath, app.notifier, app.Config(), apiErr)
				if retryErr != nil {
					log.Printf("Health check: OAuth retry failed: %v", retryErr)
				} else if result != nil {
					app.UpdateFromAuthRetry(result)
					(*eventSubClient).UpdateAccessToken(result.AccessToken)
				}
			} else {
				log.Println("Health check: API call successful, token is valid")
			}
		}

		log.Println("Health check complete")
	}

	// Run periodic application health check in background
	// This ensures the app stays running, connection is healthy, and token is refreshed
	// Runs every minute to ensure tokens remain valid
	utils.GoWithRecovery("health-check", func() {
		ticker := time.NewTicker(healthCheckInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runHealthCheck()
			}
		}
	})

	// Periodic restart: after 1 hour, trigger a graceful shutdown and re-exec the process
	// This helps recover from any accumulated state issues over long uptimes
	if app.Config().ShouldPeriodicRestart() {
		utils.GoWithRecovery("periodic-restart", func() {
			timer := time.NewTimer(periodicRestartDelay)
			defer timer.Stop()

			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				log.Println("Periodic restart: restarting application for long-running stability...")
				restartRequested.Store(true)
				utils.SendShutdownSignal()
			}
		})
	}

	// Wait for context cancellation (shutdown signal)
	// This keeps the notifier running until shutdown is requested
	<-ctx.Done()
	return nil
}

// retryOAuthFlow handles OAuth retry flow when authentication fails
// Returns updated config, token manager, and access token on success
func retryOAuthFlow(ctx context.Context, configPath string, notifier *notify.Notifier) (*config.Config, *auth.TokenManager, string, error) {
	log.Println("Starting OAuth setup automatically...")

	// Show notification about auth requirement
	if err := notifier.NotifyAuthRequired(); err != nil {
		log.Printf("Failed to send auth required notification: %v", err)
	}

	// Run OAuth setup automatically
	if err := runOAuthSetup(ctx, configPath, true); err != nil {
		return nil, nil, "", fmt.Errorf("OAuth setup failed: %w", err)
	}

	// Reload config to get the new tokens
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, nil, "", fmt.Errorf("failed to reload config after OAuth setup: %w", err)
	}

	// Create new token manager with updated tokens (reuse helper)
	tokenManager := setupTokenManager(cfg, configPath)

	// Get new access token
	accessToken, err := tokenManager.GetAccessToken(ctx)
	if err != nil {
		return nil, nil, "", fmt.Errorf("failed to get access token after OAuth setup: %w", err)
	}

	log.Println("OAuth setup complete! Authentication successful.")
	return cfg, tokenManager, accessToken, nil
}

// runOAuthSetup runs the OAuth flow to obtain and save tokens
// isAutomatic indicates if this is running automatically due to invalid token
func runOAuthSetup(ctx context.Context, configPath string, isAutomatic bool) error {
	// Load configuration
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Validate required fields
	if cfg.Twitch.ClientID == "" {
		return fmt.Errorf("Twitch Client ID is required in config")
	}
	if cfg.Twitch.ClientSecret == "" {
		return fmt.Errorf("Twitch Client Secret is required in config")
	}

	if !isAutomatic {
		fmt.Println("Starting OAuth flow...")
		fmt.Println("Make sure you have registered 'http://localhost:8080/oauth/callback' as a redirect URI in your Twitch application settings.")
		fmt.Println()
	}

	// Create OAuth flow
	oauthFlow := auth.NewOAuthFlow(cfg.Twitch.ClientID, cfg.Twitch.ClientSecret)

	// Run the flow
	tokenManager, err := oauthFlow.Run(ctx, isAutomatic)
	if err != nil {
		return fmt.Errorf("OAuth flow failed: %w", err)
	}

	// Save only tokens to preserve original config formatting
	if err := config.SaveTokens(configPath, tokenManager.AccessToken, tokenManager.RefreshToken); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Printf("\nTokens saved to %s\n", configPath)
	return nil
}

// createStreamNotification creates a StreamOnlineNotification from stream information
func createStreamNotification(channelName, channelLogin, title, gameName string) notify.StreamOnlineNotification {
	return notify.StreamOnlineNotification{
		ChannelName: channelName,
		Title:       title,
		GameName:    gameName,
		StreamURL:   utils.TwitchStreamURL(channelLogin),
	}
}

// sendStreamNotification sends a notification for a stream going online
func sendStreamNotification(notifier *notify.Notifier, notif notify.StreamOnlineNotification) {
	if err := notifier.NotifyStreamOnline(notif); err != nil {
		log.Printf("Failed to send notification: %v", err)
	}
}

// StreamInfo contains the information needed to notify about a live stream
type StreamInfo struct {
	UserName  string
	UserLogin string
	Title     string
	GameName  string
}

// notifyAndOpenStream sends a notification and optionally opens the browser for a live stream
// This is the common logic shared by EventSub handler, checkAndNotifyLiveStreams, and checkAndNotifyPolledStreams
func notifyAndOpenStream(notifier *notify.Notifier, stream StreamInfo, opts RecheckOptions) {
	notif := createStreamNotification(stream.UserName, stream.UserLogin, stream.Title, stream.GameName)
	sendStreamNotification(notifier, notif)

	// Open browser if requested and configured
	if opts.OpenBrowser && opts.BrowserOpener != nil {
		shouldOpen := opts.Config == nil || opts.Config.ShouldAutoOpen(stream.UserLogin)
		if shouldOpen {
			if err := opts.BrowserOpener.OpenTwitchStream(stream.UserLogin); err != nil {
				log.Printf("Failed to open stream: %v", err)
			}
		}
	}
}

// subscribeWithRateLimit subscribes to channels with rate limiting
func subscribeWithRateLimit(ctx context.Context, helixClient *twitch.HelixClient, eventSubClient *twitch.EventSubClient, sessionID string, existingSubs []string, allChannels []twitch.Channel, isResubscribe bool) {
	channelsToSubscribe := allChannels

	// If resubscribing, only resubscribe to already subscribed channels
	if isResubscribe {
		existingMap := make(map[string]bool)
		for _, id := range existingSubs {
			existingMap[id] = true
		}

		var filtered []twitch.Channel
		for _, ch := range allChannels {
			if existingMap[ch.ID] {
				filtered = append(filtered, ch)
			}
		}
		channelsToSubscribe = filtered
	}

	// Subscribe to all watched channels
	log.Printf("Subscribing to %d watched channels...", len(channelsToSubscribe))
	subscribeBatch(ctx, helixClient, eventSubClient, sessionID, channelsToSubscribe)
}

// subscribeBatch subscribes to a batch of channels with appropriate rate limiting
func subscribeBatch(ctx context.Context, helixClient *twitch.HelixClient, eventSubClient *twitch.EventSubClient, sessionID string, channels []twitch.Channel) {
	baseDelay := baseSubscriptionDelay

	log.Printf("Starting subscription batch: %d channels", len(channels))
	if len(channels) > 10 {
		log.Printf("⚠️  WARNING: Subscribing to %d channels, but Twitch EventSub default limit is 10 subscriptions.", len(channels))
		log.Printf("   Channels beyond the 10th may fail due to subscription cost limit.")
		log.Printf("   See README.md 'Subscription Cost Limits' section for details.")
	}

	successCount := 0
	failCount := 0

	for i := 0; i < len(channels); i++ {
		channel := channels[i]

		// Check context cancellation
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Check rate limits before making request - proactive rate limiting
		waitResult, err := helixClient.WaitForRateLimit(ctx)
		if err != nil {
			return // Context cancelled
		}
		if waitResult.Waited {
			log.Printf("Rate limit: waited %v before subscribing to %s (exhausted: %v)",
				waitResult.WaitDuration, channel.Username, waitResult.WasExhausted)
		}

		rateLimitResp, err := helixClient.CreateEventSubSubscription(ctx, sessionID, channel.ID)

		if err != nil {
			// Check for typed APIError to determine error type
			var apiErr *twitch.APIError
			if errors.As(err, &apiErr) && apiErr.IsRateLimited() {
				// 429 error - use centralized rate limit waiting
				log.Printf("Rate limited (429) for %s. Waiting for rate limit to reset...", channel.Username)

				waitResult, waitErr := helixClient.WaitForRateLimit(ctx)
				if waitErr != nil {
					return // Context cancelled
				}

				// Check if we should retry or skip this channel
				if waitResult.WasExhausted {
					rateLimit := helixClient.GetRateLimit()
					if rateLimit != nil && rateLimit.Remaining <= 0 {
						log.Printf("Rate limit still exhausted after waiting. Skipping %s for now...", channel.Username)
						continue
					}
				}

				// Retry the same channel (don't increment i)
				log.Printf("Retrying subscription for %s after rate limit wait...", channel.Username)
				i--
				continue
			}

			// Handle non-rate-limit errors
			errorBody := ""
			if apiErr != nil {
				errorBody = apiErr.GetBody()
			} else if rateLimitResp != nil {
				errorBody = string(rateLimitResp.Body)
			}

			// Check if this is a subscription cost limit error
			isCostLimitError := strings.Contains(strings.ToLower(errorBody), "subscription") &&
				(strings.Contains(strings.ToLower(errorBody), "cost") ||
					strings.Contains(strings.ToLower(errorBody), "max_total_cost") ||
					strings.Contains(strings.ToLower(errorBody), "limit"))

			if isCostLimitError {
				failCount++
				log.Printf("SUBSCRIPTION COST LIMIT REACHED for %s", channel.Username)
				log.Printf("   Twitch EventSub has a default limit of 10 subscriptions per user/client ID.")
				log.Printf("   This is the %dth channel - subscription cost limit exceeded.", i+1)
				log.Printf("   See README.md 'Subscription Cost Limits' section for details and solutions.")
				log.Printf("   Error details: %s", errorBody)
			} else {
				failCount++
				log.Printf("Failed to subscribe to %s (%s): %v", channel.Username, channel.ID, err)
				if len(errorBody) > 0 && len(errorBody) < 500 {
					log.Printf("   Error response: %s", errorBody)
				}
				// Continue to next channel on non-429 errors
			}
		} else {
			eventSubClient.MarkSubscribed(channel.ID)
			successCount++
			log.Printf("✓ Subscribed to: %s", channel.Username)

			// Log rate limit status if available
			if rateLimitResp.RateLimit != nil {
				log.Printf("Rate limit: %d/%d remaining, resets at %v",
					rateLimitResp.RateLimit.Remaining,
					rateLimitResp.RateLimit.Limit,
					rateLimitResp.RateLimit.Reset)
			}
		}

		// Dynamic delay based on rate limit remaining
		var delay time.Duration
		if rateLimitResp != nil && rateLimitResp.RateLimit != nil {
			// Calculate delay based on remaining quota - more aggressive slowdown
			if rateLimitResp.RateLimit.Limit > 0 {
				remainingPercent := float64(rateLimitResp.RateLimit.Remaining) / float64(rateLimitResp.RateLimit.Limit)
				if remainingPercent < 0.1 {
					// Very low quota - significantly increase delay
					delay = baseDelay * 3
				} else if remainingPercent < 0.2 {
					// Low quota - increase delay
					delay = baseDelay * 2
				} else if remainingPercent < 0.5 {
					// Medium-low quota - slightly increase delay
					delay = baseDelay * 3 / 2
				} else {
					delay = baseDelay
				}
			} else {
				delay = baseDelay
			}
		} else {
			delay = baseDelay
		}

		// Delay between requests (except for last one)
		if i < len(channels)-1 {
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return
			}
		}
	}

	// Summary log
	log.Printf("Subscription batch complete: %d succeeded, %d failed out of %d total", successCount, failCount, len(channels))
	if failCount > 0 && len(channels) > 10 {
		log.Printf("⚠️  Some subscriptions failed. This is likely due to Twitch's subscription cost limit (default: 10).")
		log.Printf("   See README.md 'Subscription Cost Limits' section for solutions.")
	}
}

// RecheckOptions configures the behavior of checkAndNotifyLiveStreams
type RecheckOptions struct {
	OpenBrowser       bool           // Whether to open browser for live channels
	BrowserOpener     *utils.Opener  // Browser opener (required if OpenBrowser is true)
	Config            *config.Config // Config for checking auto-open settings (required if OpenBrowser is true)
	NotifyIfNoStreams bool           // Whether to send a notification if no streams are live (for startup/refresh)
	Silent            bool           // If true, suppress all notifications (used during silent restart to only log state)
}

// checkAndNotifyLiveStreams checks which channels are already live and sends notifications
// If opts.OpenBrowser is true, it will also open the browser for live channels based on config settings
// Note: This checks all provided channels directly via the Helix API
// Returns true if any streams are live, false otherwise
func checkAndNotifyLiveStreams(ctx context.Context, helixClient *twitch.HelixClient, allChannels []twitch.Channel, notifier *notify.Notifier, opts RecheckOptions) bool {
	if len(allChannels) == 0 {
		return false
	}

	// Build a map of channel IDs to channel info for lookup
	channelMap := make(map[string]twitch.Channel)
	for _, ch := range allChannels {
		channelMap[ch.ID] = ch
	}

	// Get channel IDs from all provided channels (not filtered by subscription status)
	// This is important because at startup, subscriptions may still be in progress
	channelIDs := make([]string, 0, len(allChannels))
	for _, ch := range allChannels {
		channelIDs = append(channelIDs, ch.ID)
	}

	log.Printf("Checking which of %d channels are already live...", len(channelIDs))

	// Check which channels are live
	liveStreams, err := helixClient.GetLiveStreams(ctx, channelIDs)
	if err != nil {
		log.Printf("Failed to check live streams: %v", err)
		return false
	}

	if len(liveStreams) == 0 {
		log.Println("No channels are currently live")
		if opts.NotifyIfNoStreams {
			if err := notifier.NotifyNoStreamsLive(); err != nil {
				log.Printf("Failed to send no streams live notification: %v", err)
			}
		}
		return false
	}

	log.Printf("Found %d channels already live", len(liveStreams))

	// In silent mode, only log which channels are live without sending notifications
	if opts.Silent {
		for channelID, liveStream := range liveStreams {
			if _, ok := channelMap[channelID]; !ok {
				continue
			}
			log.Printf("Stream online (silent): %s (%s) - %s", liveStream.BroadcasterUserName, liveStream.BroadcasterUserLogin, liveStream.StreamTitle)
		}
		return true
	}

	// Send notifications for live streams
	log.Printf("Sending notifications for %d live channels...", len(liveStreams))
	for channelID, liveStream := range liveStreams {
		// Verify channel exists in our subscribed channels
		if _, ok := channelMap[channelID]; !ok {
			continue
		}

		log.Printf("Stream online: %s (%s) - %s", liveStream.BroadcasterUserName, liveStream.BroadcasterUserLogin, liveStream.StreamTitle)

		stream := StreamInfo{
			UserName:  liveStream.BroadcasterUserName,
			UserLogin: liveStream.BroadcasterUserLogin,
			Title:     liveStream.StreamTitle,
			GameName:  liveStream.GameName,
		}
		notifyAndOpenStream(notifier, stream, opts)
	}

	return true
}

// checkAndNotifyPolledStreams checks which polled channels are live and sends notifications
// This is used for manual recheck of overflow channels that are monitored via polling
// Returns true if any streams are live, false otherwise
func checkAndNotifyPolledStreams(ctx context.Context, poller *twitch.Poller, notifier *notify.Notifier, opts RecheckOptions) bool {
	polledChannels := poller.GetPolledChannels()
	if len(polledChannels) == 0 {
		return false
	}

	log.Printf("Checking which of %d polled channels are live...", len(polledChannels))

	// Force an immediate check
	liveStreams, err := poller.ForceCheck(ctx)
	if err != nil {
		log.Printf("Failed to check polled live streams: %v", err)
		return false
	}

	if len(liveStreams) == 0 {
		log.Println("No polled channels are currently live")
		if opts.NotifyIfNoStreams {
			if err := notifier.NotifyNoStreamsLive(); err != nil {
				log.Printf("Failed to send no streams live notification: %v", err)
			}
		}
		return false
	}

	log.Printf("Found %d polled channels already live", len(liveStreams))

	if opts.Silent {
		for _, liveStream := range liveStreams {
			log.Printf("Polled stream online (silent): %s (%s) - %s", liveStream.BroadcasterUserName, liveStream.BroadcasterUserLogin, liveStream.StreamTitle)
		}
		return true
	}

	log.Printf("Sending notifications for %d live polled channels...", len(liveStreams))

	// Send notifications for live streams
	for _, liveStream := range liveStreams {
		log.Printf("Polled stream online: %s (%s) - %s", liveStream.BroadcasterUserName, liveStream.BroadcasterUserLogin, liveStream.StreamTitle)

		stream := StreamInfo{
			UserName:  liveStream.BroadcasterUserName,
			UserLogin: liveStream.BroadcasterUserLogin,
			Title:     liveStream.StreamTitle,
			GameName:  liveStream.GameName,
		}
		notifyAndOpenStream(notifier, stream, opts)
	}

	return true
}
