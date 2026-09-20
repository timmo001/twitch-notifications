package twitch

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func testEventSubClient(t *testing.T, handler http.HandlerFunc, online func(StreamOnlineEvent), ready func(context.Context, string)) *EventSubClient {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := NewEventSubClient(t.Context(), online, ready)
	client.url = "ws" + strings.TrimPrefix(server.URL, "http")
	client.retryOptions.BaseDelay = time.Millisecond
	client.retryOptions.MaxDelay = 5 * time.Millisecond
	client.retryOptions.Jitter = 0
	t.Cleanup(func() { client.Close() })
	return client
}

func writeWelcome(t *testing.T, conn *websocket.Conn, id string, timeout int) {
	t.Helper()
	message := fmt.Sprintf(`{"metadata":{"message_type":"session_welcome"},"payload":{"session":{"id":%q,"status":"connected","keepalive_timeout_seconds":%d}}}`, id, timeout)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(message)); err != nil {
		t.Error(err)
	}
}

func receiveEventSubValue[T any](t *testing.T, values <-chan T) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for EventSub")
		var zero T
		return zero
	}
}

func TestEventSubReconnectsAfterReadFailure(t *testing.T) {
	for _, failure := range []string{"normal close", "abrupt close", "timeout"} {
		t.Run(failure, func(t *testing.T) {
			ready := make(chan string, 4)
			cancelled := make(chan string, 4)
			secondConnection := make(chan struct{})
			allowWelcome := make(chan struct{})
			defer close(allowWelcome)
			var connections atomic.Int32
			client := testEventSubClient(t, func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				if connections.Add(1) == 1 {
					writeWelcome(t, conn, "first", 1)
					if failure == "normal close" {
						conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
					} else if failure == "timeout" {
						conn.ReadMessage()
					}
					return
				}
				close(secondConnection)
				<-allowWelcome
				writeWelcome(t, conn, "second", 60)
				conn.ReadMessage()
			}, nil, func(ctx context.Context, id string) {
				ready <- id
				<-ctx.Done()
				cancelled <- id
			})
			if err := client.Connect(); err != nil {
				t.Fatal(err)
			}
			if id := receiveEventSubValue(t, ready); id != "first" {
				t.Fatalf("first session = %q", id)
			}
			receiveEventSubValue(t, secondConnection)
			if id := client.GetSessionID(); id != "" {
				t.Fatalf("disconnected client reports session %q", id)
			}
			if id := receiveEventSubValue(t, cancelled); id != "first" {
				t.Fatalf("cancelled session = %q", id)
			}
			allowWelcome <- struct{}{}
			if id := receiveEventSubValue(t, ready); id != "second" {
				t.Fatalf("replacement session = %q", id)
			}
			client.Close()
			if client.GetSessionID() != "" {
				t.Fatal("closed client reports a healthy session")
			}
			if id := receiveEventSubValue(t, cancelled); id != "second" {
				t.Fatalf("shutdown did not cancel replacement session: %q", id)
			}
		})
	}
}

func TestEventSubHandoverKeepsOldSocketUntilWelcome(t *testing.T) {
	ready := make(chan string, 4)
	oldClosed := make(chan struct{})
	replacementConnected := make(chan struct{})
	allowWelcome := make(chan struct{})
	defer close(allowWelcome)
	events := make(chan StreamOnlineEvent, 1)
	client := testEventSubClient(t, func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		if r.URL.Path == "/replacement" {
			close(replacementConnected)
			<-allowWelcome
			writeWelcome(t, conn, "same-session", 60)
			conn.WriteMessage(websocket.TextMessage, []byte(`{"metadata":{"message_type":"notification"},"payload":{"subscription":{"type":"stream.online"},"event":{"broadcaster_user_id":"1","broadcaster_user_login":"channel","broadcaster_user_name":"Channel","started_at":"2026-01-01T00:00:00Z"}}}`))
			conn.ReadMessage()
			return
		}
		writeWelcome(t, conn, "same-session", 60)
		message := fmt.Sprintf(`{"metadata":{"message_type":"session_reconnect"},"payload":{"session":{"reconnect_url":%q}}}`, "ws://"+r.Host+"/replacement")
		conn.WriteMessage(websocket.TextMessage, []byte(message))
		conn.ReadMessage()
		close(oldClosed)
	}, func(event StreamOnlineEvent) { events <- event }, func(_ context.Context, id string) { ready <- id })
	if err := client.Connect(); err != nil {
		t.Fatal(err)
	}
	receiveEventSubValue(t, ready)
	receiveEventSubValue(t, replacementConnected)
	select {
	case <-oldClosed:
		t.Fatal("old connection closed before replacement welcome")
	default:
	}
	allowWelcome <- struct{}{}
	receiveEventSubValue(t, oldClosed)
	if event := receiveEventSubValue(t, events); event.BroadcasterUserID != "1" {
		t.Fatalf("unexpected event: %+v", event)
	}
	client.Close()
	select {
	case id := <-ready:
		t.Fatalf("handover resubscribed to transferred session %q", id)
	default:
	}
}

func TestEventSubCloseCancelsPendingWelcome(t *testing.T) {
	connected := make(chan struct{})
	client := testEventSubClient(t, func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		close(connected)
		conn.ReadMessage()
	}, nil, nil)
	result := make(chan error, 1)
	go func() { result <- client.Connect() }()
	receiveEventSubValue(t, connected)
	closed := make(chan struct{})
	go func() {
		client.Close()
		close(closed)
	}()
	receiveEventSubValue(t, closed)
	if err := receiveEventSubValue(t, result); err == nil {
		t.Fatal("cancelled welcome succeeded")
	}
}

func TestEventSubFailedHandoverCreatesFreshSubscriptions(t *testing.T) {
	ready := make(chan string, 4)
	var connections atomic.Int32
	client := testEventSubClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/replacement" {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		if connections.Add(1) == 1 {
			writeWelcome(t, conn, "first", 60)
			message := fmt.Sprintf(`{"metadata":{"message_type":"session_reconnect"},"payload":{"session":{"reconnect_url":%q}}}`, "ws://"+r.Host+"/replacement")
			conn.WriteMessage(websocket.TextMessage, []byte(message))
		} else {
			writeWelcome(t, conn, "fresh", 60)
		}
		conn.ReadMessage()
	}, nil, func(_ context.Context, id string) { ready <- id })
	if err := client.Connect(); err != nil {
		t.Fatal(err)
	}
	if id := receiveEventSubValue(t, ready); id != "first" {
		t.Fatalf("initial session = %q", id)
	}
	if id := receiveEventSubValue(t, ready); id != "fresh" {
		t.Fatalf("fallback did not request fresh subscriptions: %q", id)
	}
}

func TestEventSubCloseCancelsReconnectBackoff(t *testing.T) {
	cancelled := make(chan struct{})
	client := testEventSubClient(t, func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		writeWelcome(t, conn, "first", 60)
	}, nil, func(ctx context.Context, _ string) {
		<-ctx.Done()
		close(cancelled)
	})
	client.retryOptions.BaseDelay = time.Hour
	client.retryOptions.MaxDelay = time.Hour
	if err := client.Connect(); err != nil {
		t.Fatal(err)
	}
	// Losing the connection cancels its subscription callback before backoff.
	receiveEventSubValue(t, cancelled)
	closed := make(chan struct{})
	go func() {
		client.Close()
		close(closed)
	}()
	receiveEventSubValue(t, closed)
}

func TestEventSubRejectsInvalidNotifications(t *testing.T) {
	called := false
	client := NewEventSubClient(t.Context(), func(StreamOnlineEvent) { called = true }, nil)
	defer client.Close()
	for _, event := range []string{
		`null`,
		`{}`,
		`{"broadcaster_user_id":42}`,
		`{"broadcaster_user_id":"1","broadcaster_user_login":"channel","broadcaster_user_name":"Channel","started_at":"invalid"}`,
	} {
		payload := fmt.Sprintf(`{"subscription":{"type":"stream.online"},"event":%s}`, event)
		if err := client.handleNotification([]byte(payload)); err == nil {
			t.Errorf("accepted invalid event %s", event)
		}
	}
	if called {
		t.Fatal("invalid event reached the notification callback")
	}
}
