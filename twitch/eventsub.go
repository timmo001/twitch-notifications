package twitch

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"twitch-notifications/utils"

	"github.com/gorilla/websocket"
)

const (
	eventsubURL = "wss://eventsub.wss.twitch.tv/ws"

	// Reconnection backoff constants
	reconnectBaseDelay = 1 * time.Second
	reconnectMaxDelay  = 2 * time.Minute
	reconnectJitter    = 0.2 // ±20% jitter

	// WebSocket timing
	wsHandshakeTimeout = 10 * time.Second
	wsReadDeadline     = 60 * time.Second
	wsCloseTimeout     = 1 * time.Second
	wsShutdownWait     = 3 * time.Second
)

// EventSubClient manages the EventSub WebSocket connection
type EventSubClient struct {
	conn             *websocket.Conn
	sessionID        string
	reconnectURL     string
	clientID         string
	accessToken      string
	subscriptions    map[string]bool // broadcaster_user_id -> subscribed
	subscriptionsMu  sync.RWMutex
	onStreamOnline   func(StreamOnlineEvent)
	onSessionReady   func(string) // Called when session is ready with session ID
	ctx              context.Context
	cancel           context.CancelFunc
	wg               sync.WaitGroup
	mu               sync.Mutex
	reconnecting     bool // Flag to prevent multiple simultaneous reconnects
	sessionNotified  bool // Track if we've notified about this session
	reconnectAttempt int  // Current reconnection attempt (for exponential backoff)
}

// StreamOnlineEvent represents a stream.online event
type StreamOnlineEvent struct {
	BroadcasterUserID    string
	BroadcasterUserLogin string
	BroadcasterUserName  string
	StreamTitle          string
	GameName             string
	StartedAt            time.Time
}

// NewEventSubClient creates a new EventSub client
func NewEventSubClient(clientID, accessToken string, onStreamOnline func(StreamOnlineEvent), onSessionReady func(string)) *EventSubClient {
	ctx, cancel := context.WithCancel(context.Background())
	return &EventSubClient{
		clientID:         clientID,
		accessToken:      accessToken,
		subscriptions:    make(map[string]bool),
		onStreamOnline:   onStreamOnline,
		onSessionReady:   onSessionReady,
		ctx:              ctx,
		cancel:           cancel,
		reconnecting:     false,
		sessionNotified:  false,
		reconnectAttempt: 0,
	}
}

// UpdateAccessToken updates the access token used for reconnections.
// This should be called when the token is refreshed to ensure reconnections use the new token.
func (esc *EventSubClient) UpdateAccessToken(accessToken string) {
	esc.mu.Lock()
	defer esc.mu.Unlock()
	esc.accessToken = accessToken
}

// Connect establishes a WebSocket connection to EventSub
func (esc *EventSubClient) Connect() error {
	esc.mu.Lock()
	defer esc.mu.Unlock()

	dialer := websocket.Dialer{
		HandshakeTimeout: wsHandshakeTimeout,
	}

	conn, _, err := dialer.Dial(eventsubURL, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to EventSub: %w", err)
	}

	esc.conn = conn

	esc.wg.Add(1)
	go esc.readMessages()

	return nil
}

// readMessages reads messages from the WebSocket connection
func (esc *EventSubClient) readMessages() {
	defer esc.wg.Done()

	// Use recover to catch panics from failed connection reads
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Panic in readMessages (connection likely failed): %v", r)
			// Don't try to reconnect on panic, just exit cleanly
		}
	}()

	for {
		// Check context cancellation first
		select {
		case <-esc.ctx.Done():
			return
		default:
		}

		// Get connection with lock to check if it's still valid
		esc.mu.Lock()
		conn := esc.conn
		esc.mu.Unlock()

		if conn == nil {
			// Connection is closed, exit
			return
		}

		// Set read deadline - Twitch sends keepalive messages periodically
		// Use a reasonable timeout that allows for network delays
		// Don't set it too short to avoid false timeouts
		if err := conn.SetReadDeadline(time.Now().Add(wsReadDeadline)); err != nil {
			log.Printf("Failed to set read deadline: %v", err)
			return
		}

		// Read message with panic recovery
		var msg map[string]interface{}
		var readErr error
		func() {
			defer func() {
				if r := recover(); r != nil {
					// Connection failed, convert panic to error
					readErr = fmt.Errorf("connection failed: %v", r)
				}
			}()
			readErr = conn.ReadJSON(&msg)
		}()

		if readErr != nil {
			// Check if context was cancelled (connection might have been closed)
			select {
			case <-esc.ctx.Done():
				return
			default:
			}

			// Check if it's a timeout (shouldn't happen often with 30s deadline)
			if isTimeout(readErr) {
				// Timeout is unexpected but not fatal, log and continue
				log.Printf("Read timeout (this shouldn't happen often): %v", readErr)
				continue
			}

			// Check if connection is closed (websocket close error)
			if websocket.IsCloseError(readErr, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket closed: %v", readErr)
				return
			}

			// Check context again in case connection was closed during read
			select {
			case <-esc.ctx.Done():
				return
			default:
			}

			// Real error or panic - be conservative about reconnecting
			// Only reconnect on actual connection failures, not transient errors
			log.Printf("Error reading message: %v", readErr)

			// Check if it's a network error that warrants reconnection
			// Don't reconnect on every error - some might be transient
			if websocket.IsUnexpectedCloseError(readErr, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				// Only reconnect if we're not already reconnecting
				esc.mu.Lock()
				shouldReconnect := !esc.reconnecting
				esc.mu.Unlock()

				if shouldReconnect {
					log.Printf("Connection closed unexpectedly, reconnecting...")
					esc.handleReconnect()
				}
			} else {
				// For other errors, just log and continue (don't reconnect aggressively)
				log.Printf("Non-fatal read error, continuing: %v", readErr)
			}
			return
		}

		if err := esc.handleMessage(msg); err != nil {
			log.Printf("Error handling message: %v", err)
		}
	}
}

// handleMessage processes incoming WebSocket messages
func (esc *EventSubClient) handleMessage(msg map[string]interface{}) error {
	metadata, ok := msg["metadata"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid message format: missing metadata")
	}

	messageType, ok := metadata["message_type"].(string)
	if !ok {
		return fmt.Errorf("invalid message format: missing message_type")
	}

	switch messageType {
	case "session_welcome":
		return esc.handleSessionWelcome(msg)
	case "session_keepalive":
		// Keepalive received, connection is healthy
		// Reset read deadline since we got a message
		esc.mu.Lock()
		conn := esc.conn
		esc.mu.Unlock()
		if conn != nil {
			conn.SetReadDeadline(time.Now().Add(wsReadDeadline))
		}
		return nil
	case "session_reconnect":
		return esc.handleSessionReconnect(msg)
	case "notification":
		return esc.handleNotification(msg)
	default:
		log.Printf("Unknown message type: %s", messageType)
		return nil
	}
}

// handleSessionWelcome processes the session welcome message
func (esc *EventSubClient) handleSessionWelcome(msg map[string]interface{}) error {
	payload, ok := msg["payload"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid welcome message format")
	}

	session, ok := payload["session"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid session data")
	}

	sessionID, ok := session["id"].(string)
	if !ok {
		return fmt.Errorf("invalid session ID")
	}

	esc.mu.Lock()
	oldSessionID := esc.sessionID
	esc.sessionID = sessionID
	esc.sessionNotified = false // Reset for new session
	esc.mu.Unlock()

	log.Printf("EventSub session established: %s", sessionID)

	// Only notify if this is a new session (not a reconnect to the same session)
	if oldSessionID != sessionID {
		// Notify that session is ready (only once per session)
		esc.mu.Lock()
		if !esc.sessionNotified && esc.onSessionReady != nil {
			esc.sessionNotified = true
			esc.mu.Unlock()
			esc.onSessionReady(sessionID)
		} else {
			esc.mu.Unlock()
		}
	}

	return nil
}

// handleSessionReconnect processes a reconnect message
func (esc *EventSubClient) handleSessionReconnect(msg map[string]interface{}) error {
	payload, ok := msg["payload"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid reconnect message format")
	}

	session, ok := payload["session"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid session data")
	}

	reconnectURL, ok := session["reconnect_url"].(string)
	if !ok {
		return fmt.Errorf("invalid reconnect URL")
	}

	esc.mu.Lock()
	esc.reconnectURL = reconnectURL
	esc.mu.Unlock()

	log.Printf("Reconnect requested, URL: %s", reconnectURL)
	esc.handleReconnect()

	return nil
}

// calculateReconnectDelay calculates the delay for reconnection using exponential backoff with jitter
func (esc *EventSubClient) calculateReconnectDelay() time.Duration {
	esc.mu.Lock()
	attempt := esc.reconnectAttempt
	esc.mu.Unlock()

	// Use shared backoff calculation (attempt is 0-based here, but CalculateBackoff expects 1-based)
	opts := utils.RetryOptions{
		BaseDelay: reconnectBaseDelay,
		MaxDelay:  reconnectMaxDelay,
		Jitter:    reconnectJitter,
	}
	return utils.CalculateBackoff(attempt+1, opts)
}

// handleReconnect reconnects to the EventSub service with exponential backoff
func (esc *EventSubClient) handleReconnect() {
	// Check if we should reconnect (context not cancelled)
	select {
	case <-esc.ctx.Done():
		return
	default:
	}

	// Prevent multiple simultaneous reconnects
	esc.mu.Lock()
	if esc.reconnecting {
		esc.mu.Unlock()
		return // Already reconnecting
	}
	esc.reconnecting = true
	oldConn := esc.conn
	esc.conn = nil // Clear connection before closing to prevent reads
	esc.mu.Unlock()

	// Close old connection if it exists
	if oldConn != nil {
		// Try to send close frame gracefully
		closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
		oldConn.WriteControl(websocket.CloseMessage, closeMsg, time.Now().Add(wsCloseTimeout))
		oldConn.Close()
	}

	url := eventsubURL
	esc.mu.Lock()
	if esc.reconnectURL != "" {
		url = esc.reconnectURL
		esc.reconnectURL = ""
	}
	accessToken := esc.accessToken // Get latest token
	esc.mu.Unlock()

	dialer := websocket.Dialer{
		HandshakeTimeout: wsHandshakeTimeout,
	}

	conn, _, err := dialer.Dial(url, nil)
	if err != nil {
		// Calculate backoff delay
		delay := esc.calculateReconnectDelay()

		esc.mu.Lock()
		esc.reconnectAttempt++
		attempt := esc.reconnectAttempt
		esc.reconnecting = false
		esc.mu.Unlock()

		log.Printf("Failed to reconnect (attempt %d): %v. Retrying in %v...", attempt, err, delay)

		// Check context before retrying
		select {
		case <-esc.ctx.Done():
			return
		case <-time.After(delay):
			go esc.handleReconnect()
		}
		return
	}

	// Successful connection - reset attempt counter
	esc.mu.Lock()
	esc.conn = conn
	esc.reconnecting = false
	esc.reconnectAttempt = 0
	esc.mu.Unlock()

	log.Printf("Successfully reconnected to EventSub (using token: %s...)", accessToken[:min(10, len(accessToken))])

	esc.wg.Add(1)
	go esc.readMessages()
}

// handleNotification processes a notification event
func (esc *EventSubClient) handleNotification(msg map[string]interface{}) error {
	payload, ok := msg["payload"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid notification format")
	}

	subscription, ok := payload["subscription"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid subscription data")
	}

	eventType, ok := subscription["type"].(string)
	if !ok || eventType != "stream.online" {
		return nil
	}

	event, ok := payload["event"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid event data")
	}

	streamEvent := StreamOnlineEvent{
		BroadcasterUserID:    getString(event, "broadcaster_user_id"),
		BroadcasterUserLogin: getString(event, "broadcaster_user_login"),
		BroadcasterUserName:  getString(event, "broadcaster_user_name"),
		StreamTitle:          getString(event, "title"),
		GameName:             getString(event, "game_name"),
	}

	if startedAtStr := getString(event, "started_at"); startedAtStr != "" {
		if t, err := time.Parse(time.RFC3339, startedAtStr); err == nil {
			streamEvent.StartedAt = t
		}
	}

	if esc.onStreamOnline != nil {
		esc.onStreamOnline(streamEvent)
	}

	return nil
}

// getString safely extracts a string from a map
func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// isTimeout checks if an error is a timeout error
func isTimeout(err error) bool {
	type timeout interface {
		Timeout() bool
	}
	if t, ok := err.(timeout); ok {
		return t.Timeout()
	}
	return false
}

// GetSessionID returns the current session ID (thread-safe)
func (esc *EventSubClient) GetSessionID() string {
	esc.mu.Lock()
	defer esc.mu.Unlock()
	return esc.sessionID
}

// MarkSubscribed marks a broadcaster as subscribed (for tracking)
func (esc *EventSubClient) MarkSubscribed(broadcasterUserID string) {
	esc.subscriptionsMu.Lock()
	defer esc.subscriptionsMu.Unlock()
	esc.subscriptions[broadcasterUserID] = true
}

// GetSubscribedChannels returns the list of subscribed channel IDs
func (esc *EventSubClient) GetSubscribedChannels() []string {
	esc.subscriptionsMu.RLock()
	defer esc.subscriptionsMu.RUnlock()
	channels := make([]string, 0, len(esc.subscriptions))
	for channelID := range esc.subscriptions {
		channels = append(channels, channelID)
	}
	return channels
}

// Close closes the WebSocket connection
func (esc *EventSubClient) Close() error {
	esc.mu.Lock()
	conn := esc.conn
	esc.mu.Unlock()

	// Close the WebSocket connection first to unblock ReadJSON
	if conn != nil {
		// Send close frame
		closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
		conn.WriteControl(websocket.CloseMessage, closeMsg, time.Now().Add(wsCloseTimeout))
		// Close the connection (this will cause ReadJSON to return)
		conn.Close()
	}

	// Cancel context to signal all goroutines to stop
	esc.cancel()

	// Wait for all goroutines to finish with timeout
	done := make(chan struct{})
	go func() {
		esc.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-time.After(wsShutdownWait):
		log.Printf("Warning: timeout waiting for goroutines to finish")
		return nil // Don't fail, just log warning
	}
}
