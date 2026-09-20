package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"twitch-notifications/config"
)

func testTokenManager(t *testing.T, handler http.HandlerFunc) *TokenManager {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	tm := NewTokenManager("client", "secret", "")
	tm.SetRefreshToken("old-refresh")
	tm.tokenURL = server.URL
	tm.httpClient = server.Client()
	tm.retryOptions.MaxAttempts = 3
	tm.retryOptions.BaseDelay = time.Millisecond
	tm.retryOptions.MaxDelay = time.Millisecond
	tm.retryOptions.Jitter = 0
	return tm
}

func TestConcurrentTokenRequestsShareRefresh(t *testing.T) {
	var requests, saves atomic.Int32
	tm := testTokenManager(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Get("refresh_token") != "old-refresh" {
			t.Errorf("unexpected refresh token %q", r.Form.Get("refresh_token"))
		}
		fmt.Fprint(w, `{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}`)
	})
	tm.SetOnTokenRefresh(func(access, refresh string) error {
		saves.Add(1)
		if access != "new-access" || refresh != "new-refresh" {
			t.Errorf("persisted incorrect tokens")
		}
		return nil
	})
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			token, err := tm.GetAccessToken(t.Context())
			if err != nil || token != "new-access" {
				t.Errorf("GetAccessToken() = %q, %v", token, err)
			}
		})
	}
	wg.Wait()
	if requests.Load() != 1 || saves.Load() != 1 {
		t.Fatalf("got %d refresh requests and %d saves, want one of each", requests.Load(), saves.Load())
	}
}

func TestTokenResponseFailuresDoNotReplaceTokens(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		body      string
		wantAuth  bool
		wantTries int32
	}{
		{"expired refresh", 400, `{"message":"Invalid refresh token"}`, true, 1},
		{"invalid client", 400, `{"message":"Invalid client secret"}`, false, 1},
		{"unauthorised", 401, `{"message":"secret-response-body"}`, true, 1},
		{"forbidden", 403, `{}`, false, 1},
		{"rate limited", 429, `{}`, false, 3},
		{"server failure", 503, `{}`, false, 3},
		{"invalid JSON", 200, `invalid`, false, 1},
		{"missing token", 200, `{"expires_in":3600}`, false, 1},
		{"invalid expiry", 200, `{"access_token":"new","expires_in":-1}`, false, 1},
		{"expiry overflow", 200, `{"access_token":"new","expires_in":9223372036854775807}`, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			tm := testTokenManager(t, func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			})
			_, err := tm.GetAccessToken(t.Context())
			if err == nil {
				t.Fatal("invalid token response succeeded")
			}
			var oauthErr *OAuthError
			isAuth := errors.As(err, &oauthErr) && oauthErr.IsAuthError()
			if isAuth != tc.wantAuth || requests.Load() != tc.wantTries {
				t.Fatalf("auth = %t, attempts = %d, error = %v", isAuth, requests.Load(), err)
			}
			if tm.AccessToken != "" || tm.RefreshToken != "old-refresh" || !tm.ExpiresAt.IsZero() {
				t.Fatal("failed response changed token state")
			}
			if strings.Contains(err.Error(), "secret-response-body") {
				t.Fatal("OAuth error leaked response body")
			}
		})
	}
}

func TestTokenPersistenceRetriesWithoutAnotherRefresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("twitch:\n  refresh_token: old-refresh\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	tm := testTokenManager(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		fmt.Fprint(w, `{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}`)
	})
	saveErr := errors.New("storage unavailable")
	saves := 0
	tm.SetOnTokenRefresh(func(access, refresh string) error {
		saves++
		if saves == 1 {
			return saveErr
		}
		return config.SaveTokens(path, access, refresh)
	})
	if _, err := tm.GetAccessToken(t.Context()); !errors.Is(err, saveErr) {
		t.Fatalf("persistence failure was hidden: %v", err)
	}
	if token, err := tm.GetAccessToken(t.Context()); err != nil || token != "new-access" {
		t.Fatalf("persistence retry failed: %q, %v", token, err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Twitch.AccessToken != "new-access" || cfg.Twitch.RefreshToken != "new-refresh" || requests.Load() != 1 || saves != 2 {
		t.Fatal("rotated tokens were not persisted without another refresh")
	}
}

func TestWaitingForTokenRefreshCanBeCancelled(t *testing.T) {
	started := make(chan struct{})
	tm := testTokenManager(t, func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		close(started)
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	first := make(chan error, 1)
	go func() {
		_, err := tm.GetAccessToken(ctx)
		first <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("refresh request did not start")
	}
	waitCtx, waitCancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer waitCancel()
	second := make(chan error, 1)
	go func() {
		_, err := tm.GetAccessToken(waitCtx)
		second <- err
	}()
	select {
	case err := <-second:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("waiting request = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waiting request ignored cancellation")
	}
	cancel()
	select {
	case err := <-first:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("active refresh = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("active refresh ignored cancellation")
	}
}
