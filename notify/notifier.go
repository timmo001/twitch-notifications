package notify

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const omarchyNotificationGlyph = "󰕃"

// Notifier handles desktop notifications
type Notifier struct {
	dbus        *DBusNotifier
	appName     string
	soundFile   string
	omarchyPath string
}

// NewNotifier creates a new notification handler
func NewNotifier(appName, soundFile string, openURL func(string) error) (*Notifier, error) {
	omarchyPath := findOmarchy()
	dbus, err := NewDBusNotifier(appName, openURL)
	if err != nil && omarchyPath == "" {
		return nil, fmt.Errorf("failed to create DBus notifier: %w", err)
	}
	if err != nil {
		log.Printf("DBus notification fallback unavailable: %v", err)
	}

	return &Notifier{
		dbus:        dbus,
		appName:     appName,
		soundFile:   soundFile,
		omarchyPath: omarchyPath,
	}, nil
}

func findOmarchy() string {
	path, err := exec.LookPath("omarchy")
	if err != nil {
		return ""
	}
	return path
}

func (n *Notifier) notify(title, body, actionURL string) error {
	if n.omarchyPath != "" {
		if err := exec.Command(n.omarchyPath, omarchyNotificationArgs(n.appName, title, body, actionURL)...).Run(); err == nil {
			return nil
		} else {
			log.Printf("Omarchy notification failed, falling back to DBus: %v", err)
		}
	}

	return n.notifyWithDBus(title, body, actionURL)
}

func omarchyNotificationArgs(appName, title, body, actionURL string) []string {
	args := []string{"notification", "send", "-g", omarchyNotificationGlyph, "--app-name", appName}
	if actionURL != "" {
		args = append(args, "--exec", omarchyOpenCommand(actionURL))
	}
	args = append(args, title)
	if body != "" {
		args = append(args, body)
	}
	return args
}

func omarchyOpenCommand(url string) string {
	command := "xdg-open"
	if os.Getenv("OMARCHY_HOST") == "desktop" {
		command = "omarchy-launch-browser"
	} else if os.Getenv("OMARCHY_HOST") == "laptop" {
		command = "omarchy-launch-webapp"
	}
	return command + " " + shellQuote(url)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func (n *Notifier) notifyWithDBus(title, body, actionURL string) error {
	if n.dbus == nil {
		return fmt.Errorf("DBus notification fallback is unavailable")
	}
	_, err := n.dbus.Notify(title, body, "", actionURL)
	if err != nil {
		return fmt.Errorf("failed to show notification: %w", err)
	}
	return nil
}

// StreamOnlineNotification represents a stream going online
type StreamOnlineNotification struct {
	ChannelName string
	Title       string
	GameName    string
	StreamURL   string
}

// playSoundIfConfigured plays the sound file if configured (non-blocking)
func (n *Notifier) playSoundIfConfigured() {
	if n.soundFile != "" {
		go func() {
			if err := n.playSound(n.soundFile); err != nil {
				log.Printf("Failed to play sound: %v", err)
			}
		}()
	}
}

// playSound attempts to play a sound file using available audio players
// Tries paplay (PulseAudio) first, then falls back to aplay (ALSA)
func (n *Notifier) playSound(soundFile string) error {
	if soundFile == "" {
		return nil
	}

	// Expand tilde and resolve absolute path
	expandedPath, err := n.expandPath(soundFile)
	if err != nil {
		return fmt.Errorf("failed to expand sound file path: %w", err)
	}

	// Check if file exists
	if _, err := os.Stat(expandedPath); os.IsNotExist(err) {
		return fmt.Errorf("sound file does not exist: %s", expandedPath)
	}

	// Try paplay first (PulseAudio - most common on modern Linux)
	if err := n.tryPlaySound("paplay", expandedPath); err == nil {
		return nil
	}

	// Fallback to aplay (ALSA)
	if err := n.tryPlaySound("aplay", expandedPath); err == nil {
		return nil
	}

	return fmt.Errorf("no audio player available (tried paplay and aplay)")
}

// tryPlaySound attempts to play a sound using the specified command
func (n *Notifier) tryPlaySound(cmd, file string) error {
	player := exec.Command(cmd, file)
	if err := player.Run(); err != nil {
		return fmt.Errorf("%s failed: %w", cmd, err)
	}
	return nil
}

// expandPath expands ~ to home directory and resolves to absolute path
func (n *Notifier) expandPath(path string) (string, error) {
	if len(path) > 0 && path[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, path[1:])
	}
	return filepath.Abs(path)
}

// NotifyStreamOnline sends a desktop notification for a stream going live
// Clicking the notification will open the stream URL in the browser
func (n *Notifier) NotifyStreamOnline(notif StreamOnlineNotification) error {
	title := fmt.Sprintf("%s is now live!", notif.ChannelName)

	var body string
	if notif.GameName != "" {
		body = fmt.Sprintf("%s\nPlaying: %s", notif.Title, notif.GameName)
	} else {
		body = notif.Title
	}

	n.playSoundIfConfigured()

	if err := n.notify(title, body, notif.StreamURL); err != nil {
		return err
	}

	log.Printf("Notification sent: %s is live (click to open: %s)", notif.ChannelName, notif.StreamURL)
	return nil
}

// NotifyStartup sends a desktop notification when the application starts
// silent controls whether to play sound (true = silent, false = play sound if configured)
func (n *Notifier) NotifyStartup(silent bool) error {
	title := fmt.Sprintf("%s is running", n.appName)
	body := "Monitoring Twitch channels for live streams"

	if !silent {
		n.playSoundIfConfigured()
	}

	if err := n.notify(title, body, ""); err != nil {
		return fmt.Errorf("failed to show startup notification: %w", err)
	}

	log.Printf("Startup notification sent")
	return nil
}

// NotifyAuthRequired sends a desktop notification when authentication is required
func (n *Notifier) NotifyAuthRequired() error {
	title := fmt.Sprintf("%s - Authentication Required", n.appName)
	body := "Opening browser for Twitch re-authorization"

	// Don't play sound for auth notification to avoid being disruptive

	if err := n.notify(title, body, ""); err != nil {
		return fmt.Errorf("failed to show auth notification: %w", err)
	}

	log.Printf("Auth required notification sent")
	return nil
}

// NotifyFatalError sends a desktop notification for fatal/non-recoverable errors
func (n *Notifier) NotifyFatalError(errorMsg string) error {
	title := fmt.Sprintf("%s - Fatal Error", n.appName)
	body := fmt.Sprintf("A fatal error occurred: %s", errorMsg)

	// Play sound for fatal errors to get user attention
	n.playSoundIfConfigured()

	if err := n.notify(title, body, ""); err != nil {
		return fmt.Errorf("failed to show fatal error notification: %w", err)
	}

	log.Printf("Fatal error notification sent: %s", errorMsg)
	return nil
}

// NotifyResumeStatus sends a desktop notification about the application status after system resume
func (n *Notifier) NotifyResumeStatus(connectionHealthy bool, tokenRefreshed bool, sessionID string) error {
	title := fmt.Sprintf("%s - Resumed from Sleep", n.appName)

	var body string
	if connectionHealthy && tokenRefreshed {
		body = fmt.Sprintf("Application refreshed successfully\nEventSub session: %s", sessionID)
	} else if connectionHealthy {
		body = fmt.Sprintf("Connection active, token refresh failed\nEventSub session: %s", sessionID)
	} else if tokenRefreshed {
		body = "Token refreshed, but EventSub connection may be inactive"
	} else {
		body = "Warning: Connection and token refresh issues detected"
	}

	// Play sound to notify user of resume status
	n.playSoundIfConfigured()

	if err := n.notify(title, body, ""); err != nil {
		return fmt.Errorf("failed to show resume status notification: %w", err)
	}

	log.Printf("Resume status notification sent")
	return nil
}

// NotifyNoStreamsLive sends a silent desktop notification when no streams are live on startup/refresh
func (n *Notifier) NotifyNoStreamsLive() error {
	title := fmt.Sprintf("%s - No Streams Live", n.appName)
	body := "No watched channels are currently live"

	// Silent notification - don't play sound

	if err := n.notify(title, body, ""); err != nil {
		return fmt.Errorf("failed to show no streams live notification: %w", err)
	}

	log.Printf("No streams live notification sent")
	return nil
}

// Close closes the notifier
func (n *Notifier) Close() error {
	if n.dbus != nil {
		return n.dbus.Close()
	}
	return nil
}
