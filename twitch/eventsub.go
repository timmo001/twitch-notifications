package twitch

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"sync"
	"time"

	"twitch-notifications/utils"

	"github.com/gorilla/websocket"
)

const (
	eventsubURL        = "wss://eventsub.wss.twitch.tv/ws"
	wsHandshakeTimeout = 10 * time.Second
	wsReadDeadline     = 60 * time.Second
	wsCloseTimeout     = time.Second
)

// EventSubClient owns its readers, reconnect attempts and subscription callbacks.
type EventSubClient struct {
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup
	mu             sync.Mutex
	conn           *websocket.Conn
	sessionID      string
	sessionCancel  context.CancelFunc
	started        bool
	reconnecting   bool
	onStreamOnline func(StreamOnlineEvent)
	onSessionReady func(context.Context, string)
	url            string
	dialer         websocket.Dialer
	retryOptions   utils.RetryOptions
}

type eventSubMessage struct {
	Metadata struct {
		MessageType string `json:"message_type"`
	} `json:"metadata"`
	Payload json.RawMessage `json:"payload"`
}

type eventSubSessionPayload struct {
	Session struct {
		ID                      string `json:"id"`
		Status                  string `json:"status"`
		KeepaliveTimeoutSeconds *int   `json:"keepalive_timeout_seconds"`
		ReconnectURL            string `json:"reconnect_url"`
	} `json:"session"`
}

// StreamOnlineEvent represents a stream.online event.
type StreamOnlineEvent struct {
	BroadcasterUserID    string    `json:"broadcaster_user_id"`
	BroadcasterUserLogin string    `json:"broadcaster_user_login"`
	BroadcasterUserName  string    `json:"broadcaster_user_name"`
	StreamTitle          string    `json:"title"`
	GameName             string    `json:"game_name"`
	ThumbnailURL         string    `json:"thumbnail_url"`
	StartedAt            time.Time `json:"started_at"`
}

func NewEventSubClient(ctx context.Context, onStreamOnline func(StreamOnlineEvent), onSessionReady func(context.Context, string)) *EventSubClient {
	ctx, cancel := context.WithCancel(ctx)
	return &EventSubClient{
		ctx:            ctx,
		cancel:         cancel,
		onStreamOnline: onStreamOnline,
		onSessionReady: onSessionReady,
		url:            eventsubURL,
		dialer:         websocket.Dialer{HandshakeTimeout: wsHandshakeTimeout},
		retryOptions: utils.RetryOptions{
			BaseDelay: time.Second,
			MaxDelay:  2 * time.Minute,
			Jitter:    0.2,
		},
	}
}

// Connect waits for a valid welcome before starting the reader and subscriptions.
func (esc *EventSubClient) Connect() error {
	esc.mu.Lock()
	if err := esc.ctx.Err(); err != nil {
		esc.mu.Unlock()
		return err
	}
	if esc.started {
		esc.mu.Unlock()
		return fmt.Errorf("EventSub client already started")
	}
	esc.started = true
	esc.wg.Add(1)
	esc.mu.Unlock()
	defer esc.wg.Done()

	conn, sessionID, timeout, err := esc.dial(esc.url)
	if err != nil {
		return fmt.Errorf("failed to connect to EventSub: %w", err)
	}
	return esc.activate(conn, sessionID, timeout, false)
}

func (esc *EventSubClient) dial(endpoint string) (*websocket.Conn, string, time.Duration, error) {
	conn, resp, err := esc.dialer.DialContext(esc.ctx, endpoint, nil)
	if err != nil {
		if resp != nil {
			resp.Body.Close()
		}
		return nil, "", 0, err
	}
	// The connection is not published yet, so cancellation must also close it
	// while waiting for the welcome message.
	stop := context.AfterFunc(esc.ctx, func() { conn.Close() })
	defer stop()
	conn.SetReadLimit(1 << 20)
	if err := conn.SetReadDeadline(time.Now().Add(wsHandshakeTimeout)); err != nil {
		conn.Close()
		return nil, "", 0, err
	}
	var msg eventSubMessage
	if err := conn.ReadJSON(&msg); err != nil {
		conn.Close()
		return nil, "", 0, fmt.Errorf("read EventSub welcome: %w", err)
	}
	var payload eventSubSessionPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil || msg.Metadata.MessageType != "session_welcome" || payload.Session.ID == "" || payload.Session.Status != "connected" {
		conn.Close()
		return nil, "", 0, fmt.Errorf("invalid EventSub welcome")
	}
	timeout := wsReadDeadline
	if seconds := payload.Session.KeepaliveTimeoutSeconds; seconds != nil {
		if *seconds <= 0 || *seconds > 600 {
			conn.Close()
			return nil, "", 0, fmt.Errorf("invalid EventSub keepalive timeout")
		}
		timeout = time.Duration(*seconds) * time.Second
	}
	return conn, payload.Session.ID, timeout, nil
}

func (esc *EventSubClient) activate(conn *websocket.Conn, sessionID string, timeout time.Duration, transferred bool) error {
	esc.mu.Lock()
	if err := esc.ctx.Err(); err != nil {
		esc.mu.Unlock()
		conn.Close()
		return err
	}
	oldConn := esc.conn
	esc.conn = conn
	esc.sessionID = sessionID
	esc.reconnecting = false
	var sessionCtx context.Context
	if !transferred {
		if esc.sessionCancel != nil {
			esc.sessionCancel()
		}
		sessionCtx, esc.sessionCancel = context.WithCancel(esc.ctx)
	}
	esc.wg.Add(1)
	if !transferred && esc.onSessionReady != nil {
		esc.wg.Add(1)
	}
	esc.mu.Unlock()

	// Twitch transfers subscriptions during a requested handover. Close the old
	// socket only after receiving the replacement's welcome.
	if oldConn != nil {
		oldConn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(wsCloseTimeout))
		oldConn.Close()
	}
	log.Printf("EventSub session established: %s", sessionID)
	go esc.readMessages(conn, timeout)
	if !transferred && esc.onSessionReady != nil {
		go func() {
			defer esc.wg.Done()
			esc.onSessionReady(sessionCtx, sessionID)
		}()
	}
	return nil
}

func (esc *EventSubClient) readMessages(conn *websocket.Conn, timeout time.Duration) {
	defer esc.wg.Done()
	stop := context.AfterFunc(esc.ctx, func() { conn.Close() })
	defer stop()
	for esc.ctx.Err() == nil {
		if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
			esc.reconnect(conn, "")
			return
		}
		var msg eventSubMessage
		if err := conn.ReadJSON(&msg); err != nil {
			// All read failures, including timeouts, require a new socket.
			if esc.ctx.Err() == nil {
				log.Printf("EventSub read failed: %v", err)
				esc.reconnect(conn, "")
			}
			return
		}
		switch msg.Metadata.MessageType {
		case "session_keepalive":
		case "session_reconnect":
			var payload eventSubSessionPayload
			if err := json.Unmarshal(msg.Payload, &payload); err != nil {
				log.Printf("Invalid EventSub reconnect message: %v", err)
				continue
			}
			endpoint, err := url.Parse(payload.Session.ReconnectURL)
			original, _ := url.Parse(esc.url)
			if err != nil || endpoint.Host != original.Host || endpoint.Scheme != original.Scheme || endpoint.User != nil {
				log.Print("Invalid EventSub reconnect URL")
				continue
			}
			esc.reconnect(conn, payload.Session.ReconnectURL)
		case "notification":
			if err := esc.handleNotification(msg.Payload); err != nil {
				log.Printf("Invalid EventSub notification: %v", err)
			}
		default:
			log.Printf("Unknown EventSub message type: %q", msg.Metadata.MessageType)
		}
	}
}

func (esc *EventSubClient) reconnect(oldConn *websocket.Conn, endpoint string) {
	esc.mu.Lock()
	if esc.ctx.Err() != nil || esc.conn != oldConn {
		esc.mu.Unlock()
		return
	}
	if endpoint == "" {
		esc.conn = nil
		esc.sessionID = ""
		if esc.sessionCancel != nil {
			esc.sessionCancel()
		}
		oldConn.Close()
	}
	if esc.reconnecting {
		esc.mu.Unlock()
		return
	}
	esc.reconnecting = true
	esc.wg.Add(1)
	esc.mu.Unlock()

	go func() {
		defer esc.wg.Done()
		transferred := endpoint != ""
		if endpoint == "" {
			endpoint = esc.url
		}
		for attempt := 1; esc.ctx.Err() == nil; attempt++ {
			// Requested handovers start immediately; ordinary reconnects back off
			// even if the server repeatedly welcomes and then closes the socket.
			if !transferred {
				select {
				case <-esc.ctx.Done():
					return
				case <-time.After(utils.CalculateBackoff(attempt, esc.retryOptions)):
				}
			}
			conn, sessionID, timeout, err := esc.dial(endpoint)
			if err == nil {
				esc.activate(conn, sessionID, timeout, transferred)
				return
			}
			// A failed handover falls back to a fresh session and subscriptions.
			endpoint = esc.url
			transferred = false
			log.Printf("EventSub reconnect failed: %v", err)
		}
	}()
}

func (esc *EventSubClient) handleNotification(data json.RawMessage) error {
	var payload struct {
		Subscription struct {
			Type string `json:"type"`
		} `json:"subscription"`
		Event json.RawMessage `json:"event"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	if payload.Subscription.Type == "" {
		return fmt.Errorf("missing subscription type")
	}
	if payload.Subscription.Type != "stream.online" {
		return nil
	}
	var event StreamOnlineEvent
	if err := json.Unmarshal(payload.Event, &event); err != nil {
		return err
	}
	if event.BroadcasterUserID == "" || event.BroadcasterUserLogin == "" || event.BroadcasterUserName == "" || event.StartedAt.IsZero() {
		return fmt.Errorf("missing stream.online fields")
	}
	if esc.onStreamOnline != nil {
		esc.onStreamOnline(event)
	}
	return nil
}

// GetSessionID returns an empty ID when disconnected or shutting down.
func (esc *EventSubClient) GetSessionID() string {
	esc.mu.Lock()
	defer esc.mu.Unlock()
	if esc.ctx.Err() != nil || esc.conn == nil {
		return ""
	}
	return esc.sessionID
}

func (esc *EventSubClient) Close() error {
	esc.mu.Lock()
	esc.cancel()
	conn := esc.conn
	esc.conn = nil
	esc.sessionID = ""
	esc.mu.Unlock()
	if conn != nil {
		conn.Close()
	}
	esc.wg.Wait()
	return nil
}
