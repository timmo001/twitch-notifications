package tray

import (
	_ "embed"
	"fmt"
	"log"
	"sync"

	"twitch-notifications/config"
	"twitch-notifications/utils"

	"fyne.io/systray"
)

//go:embed icon.png
var trayIconPngData []byte

// maxLiveChannelItems is the number of pre-allocated sub-menu items
// for displaying live channels under the status header.
const maxLiveChannelItems = 50

// LiveChannel represents a currently live channel for display in the tray menu.
type LiveChannel struct {
	DisplayName string // e.g. "channel_a: Stream Title"
	Login       string // e.g. "channel_a" (used for twitch.tv URL)
}

// channelItem pairs a pre-allocated sub-menu item with the login of the
// channel it currently represents.
type channelItem struct {
	item  *systray.MenuItem
	login string
}

var (
	recheckHandler     func()
	recheckMu          sync.RWMutex
	recheckOpenHandler func()
	recheckOpenMu      sync.RWMutex
	restartHandler     func()
	restartMu          sync.RWMutex
	statusHandler      func() []LiveChannel
	statusMu           sync.RWMutex
	openStreamHandler  func(login string)
	openStreamMu       sync.RWMutex
	logPathHandler     func() string
	logPathMu          sync.RWMutex
	launchTUIHandler   func()
	launchTUIMu        sync.RWMutex
)

func SetRecheckHandler(handler func()) {
	recheckMu.Lock()
	defer recheckMu.Unlock()
	recheckHandler = handler
}

func getRecheckHandler() func() {
	recheckMu.RLock()
	defer recheckMu.RUnlock()
	return recheckHandler
}

func SetRecheckOpenHandler(handler func()) {
	recheckOpenMu.Lock()
	defer recheckOpenMu.Unlock()
	recheckOpenHandler = handler
}

func getRecheckOpenHandler() func() {
	recheckOpenMu.RLock()
	defer recheckOpenMu.RUnlock()
	return recheckOpenHandler
}

func SetRestartHandler(handler func()) {
	restartMu.Lock()
	defer restartMu.Unlock()
	restartHandler = handler
}

func getRestartHandler() func() {
	restartMu.RLock()
	defer restartMu.RUnlock()
	return restartHandler
}

func SetStatusHandler(handler func() []LiveChannel) {
	statusMu.Lock()
	defer statusMu.Unlock()
	statusHandler = handler
}

func getStatusHandler() func() []LiveChannel {
	statusMu.RLock()
	defer statusMu.RUnlock()
	return statusHandler
}

func SetOpenStreamHandler(handler func(login string)) {
	openStreamMu.Lock()
	defer openStreamMu.Unlock()
	openStreamHandler = handler
}

func getOpenStreamHandler() func(login string) {
	openStreamMu.RLock()
	defer openStreamMu.RUnlock()
	return openStreamHandler
}

func SetLogPathHandler(handler func() string) {
	logPathMu.Lock()
	defer logPathMu.Unlock()
	logPathHandler = handler
}

func getLogPathHandler() func() string {
	logPathMu.RLock()
	defer logPathMu.RUnlock()
	return logPathHandler
}

func SetLaunchTUIHandler(handler func()) {
	launchTUIMu.Lock()
	defer launchTUIMu.Unlock()
	launchTUIHandler = handler
}

func getLaunchTUIHandler() func() {
	launchTUIMu.RLock()
	defer launchTUIMu.RUnlock()
	return launchTUIHandler
}

// refreshCh allows callers to trigger a status update via RefreshStatus().
var refreshCh = make(chan struct{}, 1)

// RefreshStatus triggers a tray status update.
// Non-blocking: if a refresh is already pending, the call is a no-op.
func RefreshStatus() {
	select {
	case refreshCh <- struct{}{}:
	default:
	}
}

// OnReady is called when the system tray is ready
func OnReady() {
	systray.SetIcon(trayIconPngData)
	systray.SetTitle("Twitch Notifier")
	systray.SetTooltip("Twitch Live Notifier")

	// ── Status ──
	mStatus := systray.AddMenuItem("No channels live", "Live channel status")

	// Pre-allocate sub-menu items for live channels (hidden by default).
	// systray only supports adding items during OnReady, so we pool them.
	channelItems := make([]channelItem, maxLiveChannelItems)
	var channelItemsMu sync.RWMutex
	for i := range channelItems {
		sub := mStatus.AddSubMenuItem("", "Click to open stream")
		sub.Hide()
		channelItems[i] = channelItem{item: sub}
	}

	systray.AddSeparator()

	// ── Actions ──
	mRecheck := systray.AddMenuItem("Recheck Live", "Check for live channels now")
	mRecheckOpen := systray.AddMenuItem("Recheck & Open", "Check for live channels and open in browser")

	systray.AddSeparator()

	// ── Tools ──
	mOpenConfig := systray.AddMenuItem("Open Config", "Open configuration file")
	mOpenChannels := systray.AddMenuItem("Open Channels", "Open channels file")
	mOpenLog := systray.AddMenuItem("Open Log", "Open today's log file")
	mLaunchTUI := systray.AddMenuItem("Launch TUI", "Open the TUI in a terminal")

	systray.AddSeparator()

	// ── Application ──
	mRestart := systray.AddMenuItem("Restart", "Restart the application")
	mQuit := systray.AddMenuItem("Quit", "Quit the application")

	// Wait for RefreshStatus() calls and update the menu
	go awaitStatusRefresh(mStatus, channelItems, &channelItemsMu)

	// Handle menu item clicks
	go handleMenuClicks(mRecheck, mRecheckOpen, mOpenConfig, mOpenChannels, mOpenLog, mLaunchTUI, mRestart, mQuit)

	// Handle clicks on pre-allocated live channel sub-menu items
	for i := range channelItems {
		idx := i
		go func() {
			for range channelItems[idx].item.ClickedCh {
				channelItemsMu.RLock()
				login := channelItems[idx].login
				channelItemsMu.RUnlock()
				if login == "" {
					continue
				}
				log.Printf("Opening stream: %s", login)
				if handler := getOpenStreamHandler(); handler != nil {
					go handler(login)
				}
			}
		}()
	}
}

func handleMenuClicks(mRecheck, mRecheckOpen, mOpenConfig, mOpenChannels, mOpenLog, mLaunchTUI, mRestart, mQuit *systray.MenuItem) {
	for {
		select {
		case <-mRecheck.ClickedCh:
			if handler := getRecheckHandler(); handler != nil {
				go handler()
			}

		case <-mRecheckOpen.ClickedCh:
			if handler := getRecheckOpenHandler(); handler != nil {
				go handler()
			}

		case <-mOpenConfig.ClickedCh:
			configPath := config.GetConfigPath()
			if configPath == "" {
				log.Printf("Config path not set")
				continue
			}
			if err := utils.OpenFile(configPath); err != nil {
				log.Printf("Failed to open config: %v", err)
			}

		case <-mOpenChannels.ClickedCh:
			channelsPath := config.GetChannelsPath()
			if channelsPath == "" {
				log.Printf("Channels path not set")
				continue
			}
			if err := utils.OpenFile(channelsPath); err != nil {
				log.Printf("Failed to open channels: %v", err)
			}

		case <-mOpenLog.ClickedCh:
			if handler := getLogPathHandler(); handler != nil {
				if logPath := handler(); logPath != "" {
					if err := utils.OpenFile(logPath); err != nil {
						log.Printf("Failed to open log: %v", err)
					}
				}
			}

		case <-mLaunchTUI.ClickedCh:
			if handler := getLaunchTUIHandler(); handler != nil {
				go handler()
			}

		case <-mRestart.ClickedCh:
			if handler := getRestartHandler(); handler != nil {
				go handler()
			}

		case <-mQuit.ClickedCh:
			systray.Quit()
			if err := utils.SendShutdownSignal(); err != nil {
				log.Printf("Failed to send shutdown signal: %v", err)
			}
			return
		}
	}
}

func awaitStatusRefresh(mStatus *systray.MenuItem, channelItems []channelItem, channelItemsMu *sync.RWMutex) {
	for range refreshCh {
		updateStatus(mStatus, channelItems, channelItemsMu)
	}
}

// maxMenuItemLen is the maximum character length for a sub-menu item title.
const maxMenuItemLen = 60

func updateStatus(mStatus *systray.MenuItem, channelItems []channelItem, channelItemsMu *sync.RWMutex) {
	handler := getStatusHandler()
	if handler == nil {
		return
	}

	channels := handler()
	count := len(channels)

	if count == 0 {
		mStatus.SetTitle("No channels live")
		systray.SetTooltip("Twitch Live Notifier")
	} else {
		label := fmt.Sprintf("Live: %d channel", count)
		if count != 1 {
			label += "s"
		}
		mStatus.SetTitle(label)
		systray.SetTooltip(fmt.Sprintf("Twitch Live Notifier - %s", label))
	}

	channelItemsMu.Lock()
	for i := range channelItems {
		if i < count {
			channelItems[i].item.SetTitle(truncate(channels[i].DisplayName, maxMenuItemLen))
			channelItems[i].item.SetTooltip(channels[i].DisplayName)
			channelItems[i].login = channels[i].Login
			channelItems[i].item.Show()
		} else {
			channelItems[i].item.Hide()
			channelItems[i].login = ""
		}
	}
	channelItemsMu.Unlock()
}

func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "…"
}

// OnExit is called when the system tray is exiting
func OnExit() {
	// Cleanup if needed
}
