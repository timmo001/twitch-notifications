package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"twitch-notifications/utils"
)

// httpClientTimeout uses the shared constant for HTTP requests
var httpClientTimeout = utils.HTTPClientTimeout

// Token timing constants
const (
	tokenExpiryBuffer = 5 * time.Minute // Refresh tokens before they expire
)

// TokenRefreshCallback is called when tokens are refreshed
// It receives the new access token and refresh token
type TokenRefreshCallback func(accessToken, refreshToken string)

// tokenResponse represents the response from Twitch OAuth token endpoint
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

// TokenManager handles OAuth token management
type TokenManager struct {
	ClientID       string
	ClientSecret   string
	AccessToken    string
	RefreshToken   string
	ExpiresAt      time.Time
	OnTokenRefresh TokenRefreshCallback // Called when tokens are refreshed
	mu             sync.RWMutex         // Protects token fields
	refreshMu      sync.Mutex           // Prevents concurrent refresh operations
}

// SetRefreshToken sets the refresh token
func (tm *TokenManager) SetRefreshToken(refreshToken string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.RefreshToken = refreshToken
}

// SetOnTokenRefresh sets the callback for token refresh events
func (tm *TokenManager) SetOnTokenRefresh(callback TokenRefreshCallback) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.OnTokenRefresh = callback
}

// NewTokenManager creates a new token manager
func NewTokenManager(clientID, clientSecret, accessToken string) *TokenManager {
	return &TokenManager{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		AccessToken:  accessToken,
	}
}

// ValidateToken checks if the current token is valid
func (tm *TokenManager) ValidateToken() bool {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.validateTokenLocked()
}

// validateTokenLocked checks if the current token is valid (caller must hold lock)
func (tm *TokenManager) validateTokenLocked() bool {
	if tm.AccessToken == "" {
		return false
	}
	// If ExpiresAt is zero, assume token is valid (no expiration info)
	if tm.ExpiresAt.IsZero() {
		return true
	}
	return time.Now().Before(tm.ExpiresAt.Add(-tokenExpiryBuffer))
}

// GetAccessToken returns a valid access token, refreshing if necessary.
// This method uses retry with exponential backoff for transient errors.
// It serializes refresh operations to prevent race conditions where multiple
// goroutines attempt to refresh simultaneously, which could invalidate tokens.
func (tm *TokenManager) GetAccessToken(ctx context.Context) (string, error) {
	// Fast path: check if token is already valid
	tm.mu.RLock()
	if tm.validateTokenLocked() {
		token := tm.AccessToken
		tm.mu.RUnlock()
		return token, nil
	}
	hasRefreshToken := tm.RefreshToken != ""
	tm.mu.RUnlock()

	if !hasRefreshToken {
		return "", fmt.Errorf("no valid access token and no refresh token available")
	}

	// Serialize refresh operations to prevent concurrent refreshes.
	// This is critical because Twitch refresh tokens are one-time use:
	// using an already-used refresh token can invalidate all tokens.
	tm.refreshMu.Lock()
	defer tm.refreshMu.Unlock()

	// Double-check: another goroutine may have refreshed while we waited for the lock
	tm.mu.RLock()
	if tm.validateTokenLocked() {
		token := tm.AccessToken
		tm.mu.RUnlock()
		return token, nil
	}
	tm.mu.RUnlock()

	// Now safe to refresh - we hold the refresh lock
	if err := tm.RefreshAccessTokenWithRetry(ctx); err != nil {
		return "", fmt.Errorf("failed to refresh token: %w", err)
	}

	tm.mu.RLock()
	token := tm.AccessToken
	tm.mu.RUnlock()
	return token, nil
}

// RefreshAccessToken refreshes the access token using the refresh token.
// This is the low-level refresh without retry logic.
func (tm *TokenManager) RefreshAccessToken(ctx context.Context) error {
	tm.mu.RLock()
	refreshToken := tm.RefreshToken
	clientID := tm.ClientID
	clientSecret := tm.ClientSecret
	tm.mu.RUnlock()

	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", refreshToken)
	data.Set("client_id", clientID)
	data.Set("client_secret", clientSecret)

	req, err := http.NewRequestWithContext(ctx, "POST", "https://id.twitch.tv/oauth2/token", strings.NewReader(data.Encode()))
	if err != nil {
		// Request creation errors are not retryable
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: httpClientTimeout}
	resp, err := client.Do(req)
	if err != nil {
		// Network errors are retryable
		return utils.NewRetryableError(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		err := fmt.Errorf("token refresh failed (status %d): %s", resp.StatusCode, string(body))

		// 401/403 errors are not retryable (invalid credentials)
		// 5xx errors and 429 are retryable
		if resp.StatusCode >= 500 || resp.StatusCode == 429 {
			return utils.NewRetryableError(err)
		}
		return err
	}

	var tokenResp tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return err
	}

	// Update tokens with lock
	tm.mu.Lock()
	tm.AccessToken = tokenResp.AccessToken
	if tokenResp.RefreshToken != "" {
		tm.RefreshToken = tokenResp.RefreshToken
	}
	tm.ExpiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	callback := tm.OnTokenRefresh
	newAccessToken := tm.AccessToken
	newRefreshToken := tm.RefreshToken
	tm.mu.Unlock()

	// Call persistence callback if set (outside lock to avoid deadlocks)
	if callback != nil {
		callback(newAccessToken, newRefreshToken)
	}

	return nil
}

// RefreshAccessTokenWithRetry refreshes the access token with exponential backoff retry.
// It retries on transient errors (network issues, 5xx, 429) but not on auth errors (401/403).
func (tm *TokenManager) RefreshAccessTokenWithRetry(ctx context.Context) error {
	opts := utils.RetryOptions{
		MaxAttempts: 10,
		BaseDelay:   1 * time.Second,
		MaxDelay:    5 * time.Minute,
		Jitter:      0.2,
	}

	attempt := 0
	return utils.Retry(ctx, func() error {
		attempt++
		err := tm.RefreshAccessToken(ctx)
		if err != nil {
			if utils.IsRetryable(err) {
				log.Printf("Token refresh attempt %d failed (will retry): %v", attempt, err)
			}
			return err
		}
		if attempt > 1 {
			log.Printf("Token refresh succeeded on attempt %d", attempt)
		}
		return nil
	}, opts)
}

// GetAuthorizationURL generates the OAuth authorization URL
func GetAuthorizationURL(clientID, redirectURI string, scopes []string) string {
	params := url.Values{}
	params.Set("client_id", clientID)
	params.Set("redirect_uri", redirectURI)
	params.Set("response_type", "code")
	params.Set("scope", strings.Join(scopes, " "))

	return fmt.Sprintf("https://id.twitch.tv/oauth2/authorize?%s", params.Encode())
}

// ExchangeCodeForToken exchanges an authorization code for an access token
func ExchangeCodeForToken(ctx context.Context, clientID, clientSecret, code, redirectURI string) (*TokenManager, error) {
	data := url.Values{}
	data.Set("client_id", clientID)
	data.Set("client_secret", clientSecret)
	data.Set("code", code)
	data.Set("grant_type", "authorization_code")
	data.Set("redirect_uri", redirectURI)

	req, err := http.NewRequestWithContext(ctx, "POST", "https://id.twitch.tv/oauth2/token", strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: httpClientTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("token exchange failed: %s", string(body))
	}

	var tokenResp tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, err
	}

	tm := &TokenManager{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second),
	}

	return tm, nil
}
