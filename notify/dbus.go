package notify

import (
	"fmt"
	"log"
	"sync"

	"github.com/godbus/dbus/v5"
)

const (
	dbusInterface   = "org.freedesktop.Notifications"
	dbusPath        = "/org/freedesktop/Notifications"
	dbusDestination = "org.freedesktop.Notifications"
)

// DBusNotifier handles desktop notifications via DBus with action support
type DBusNotifier struct {
	conn    *dbus.Conn
	appName string

	// Track notification IDs to their associated URLs for click handling
	mu           sync.RWMutex
	notifActions map[uint32]string // notificationID -> URL to open

	// Browser opener function
	openURL func(string) error

	// Done channel to stop the signal listener
	done chan struct{}
}

// NewDBusNotifier creates a new DBus notification handler
func NewDBusNotifier(appName string, openURL func(string) error) (*DBusNotifier, error) {
	// Use a private connection so we can safely close it without affecting
	// other DBus users (like systray) that use the shared session bus
	conn, err := dbus.SessionBusPrivate()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to session bus: %w", err)
	}

	// Authenticate with the bus
	if err = conn.Auth(nil); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to authenticate with session bus: %w", err)
	}

	// Say hello to the bus
	if err = conn.Hello(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to send Hello to session bus: %w", err)
	}

	n := &DBusNotifier{
		conn:         conn,
		appName:      appName,
		notifActions: make(map[uint32]string),
		openURL:      openURL,
		done:         make(chan struct{}),
	}

	// Start listening for action signals
	if err := n.listenForActions(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to setup action listener: %w", err)
	}

	return n, nil
}

// listenForActions sets up a listener for ActionInvoked and NotificationClosed signals
func (n *DBusNotifier) listenForActions() error {
	// Add match rules for the signals we care about
	if err := n.conn.AddMatchSignal(
		dbus.WithMatchInterface(dbusInterface),
		dbus.WithMatchMember("ActionInvoked"),
	); err != nil {
		return fmt.Errorf("failed to add ActionInvoked match: %w", err)
	}

	if err := n.conn.AddMatchSignal(
		dbus.WithMatchInterface(dbusInterface),
		dbus.WithMatchMember("NotificationClosed"),
	); err != nil {
		return fmt.Errorf("failed to add NotificationClosed match: %w", err)
	}

	// Start goroutine to handle signals
	signals := make(chan *dbus.Signal, 10)
	n.conn.Signal(signals)

	go func() {
		for {
			select {
			case sig := <-signals:
				if sig == nil {
					return
				}
				n.handleSignal(sig)
			case <-n.done:
				return
			}
		}
	}()

	return nil
}

// handleSignal processes DBus signals
func (n *DBusNotifier) handleSignal(sig *dbus.Signal) {
	switch sig.Name {
	case dbusInterface + ".ActionInvoked":
		if len(sig.Body) >= 2 {
			notifID, ok1 := sig.Body[0].(uint32)
			actionKey, ok2 := sig.Body[1].(string)
			if ok1 && ok2 {
				n.handleAction(notifID, actionKey)
			}
		}
	case dbusInterface + ".NotificationClosed":
		if len(sig.Body) >= 1 {
			notifID, ok := sig.Body[0].(uint32)
			if ok {
				n.cleanupNotification(notifID)
			}
		}
	}
}

// handleAction is called when a notification action is invoked
func (n *DBusNotifier) handleAction(notifID uint32, actionKey string) {
	n.mu.RLock()
	url, exists := n.notifActions[notifID]
	n.mu.RUnlock()

	if !exists {
		log.Printf("Action invoked for unknown notification %d", notifID)
		return
	}

	if actionKey == "default" && url != "" {
		log.Printf("Opening URL from notification click: %s", url)
		if err := n.openURL(url); err != nil {
			log.Printf("Failed to open URL: %v", err)
		}
	}

	// Clean up after action is handled
	n.cleanupNotification(notifID)
}

// cleanupNotification removes a notification from tracking
func (n *DBusNotifier) cleanupNotification(notifID uint32) {
	n.mu.Lock()
	delete(n.notifActions, notifID)
	n.mu.Unlock()
}

// Notify sends a notification via DBus
// If actionURL is non-empty, clicking the notification will open that URL
func (n *DBusNotifier) Notify(summary, body, icon, actionURL string) (uint32, error) {
	obj := n.conn.Object(dbusDestination, dbusPath)

	// Build actions array
	// "default" is a special action key that's invoked when clicking the notification body
	var actions []string
	if actionURL != "" {
		actions = []string{"default", "Open Stream"}
	}

	// Hints can include things like urgency, category, etc.
	hints := map[string]dbus.Variant{
		"urgency": dbus.MakeVariant(byte(1)), // Normal urgency
	}

	// Timeout: -1 means use notification daemon's default
	timeout := int32(-1)

	// Call the Notify method
	// Signature: Notify(app_name, replaces_id, app_icon, summary, body, actions, hints, expire_timeout) -> notification_id
	call := obj.Call(
		dbusInterface+".Notify",
		0,
		n.appName, // app_name
		uint32(0), // replaces_id (0 = new notification)
		icon,      // app_icon
		summary,   // summary
		body,      // body
		actions,   // actions
		hints,     // hints
		timeout,   // expire_timeout
	)

	if call.Err != nil {
		return 0, fmt.Errorf("Notify call failed: %w", call.Err)
	}

	var notifID uint32
	if err := call.Store(&notifID); err != nil {
		return 0, fmt.Errorf("failed to get notification ID: %w", err)
	}

	// Track the URL for this notification if provided
	if actionURL != "" {
		n.mu.Lock()
		n.notifActions[notifID] = actionURL
		n.mu.Unlock()
	}

	return notifID, nil
}

// Close closes the DBus connection
func (n *DBusNotifier) Close() error {
	close(n.done)
	if n.conn != nil {
		return n.conn.Close()
	}
	return nil
}
