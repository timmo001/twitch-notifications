package auth

import (
	"context"
	"encoding/json"
	"errors"
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

// Token timing constants
const (
	tokenExpiryBuffer = 5 * time.Minute // Refresh tokens before they expire
	tokenEndpoint     = "https://id.twitch.tv/oauth2/token"
)

var ErrMissingToken = errors.New("no valid access token and no refresh token available")

// OAuthError excludes response bodies so credentials cannot leak through logs.
type OAuthError struct {
	StatusCode          int
	InvalidRefreshToken bool
}

func (e *OAuthError) Error() string {
	if e.InvalidRefreshToken {
		return "OAuth refresh token is invalid"
	}
	return fmt.Sprintf("OAuth request failed (status %d)", e.StatusCode)
}

func (e *OAuthError) IsAuthError() bool {
	return e.StatusCode == http.StatusUnauthorized || e.InvalidRefreshToken
}

// TokenRefreshCallback is called when tokens are refreshed
// It receives the new access token and refresh token
type TokenRefreshCallback func(accessToken, refreshToken string) error

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
	refresh        chan struct{}        // Serialises refreshes with cancellable waiting
	tokensDirty    bool
	httpClient     *http.Client
	tokenURL       string
	retryOptions   utils.RetryOptions
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
		refresh:      make(chan struct{}, 1),
		httpClient:   &http.Client{Timeout: utils.HTTPClientTimeout},
		tokenURL:     tokenEndpoint,
		retryOptions: utils.DefaultRetryOptions(),
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
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// Fast path: check if token is already valid
	tm.mu.RLock()
	if tm.validateTokenLocked() && !tm.tokensDirty {
		token := tm.AccessToken
		tm.mu.RUnlock()
		return token, nil
	}
	hasRefreshToken := tm.RefreshToken != ""
	tm.mu.RUnlock()

	if !hasRefreshToken {
		return "", ErrMissingToken
	}

	select {
	case tm.refresh <- struct{}{}:
		defer func() { <-tm.refresh }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := tm.persistTokens(); err != nil {
		return "", err
	}

	// Double-check: another goroutine may have refreshed while we waited for the lock
	tm.mu.RLock()
	if tm.validateTokenLocked() {
		token := tm.AccessToken
		tm.mu.RUnlock()
		return token, nil
	}
	tm.mu.RUnlock()

	// Now safe to refresh - we hold the refresh lock
	if err := tm.refreshAccessTokenWithRetry(ctx); err != nil {
		return "", fmt.Errorf("failed to refresh token: %w", err)
	}
	if err := tm.persistTokens(); err != nil {
		return "", err
	}

	tm.mu.RLock()
	token := tm.AccessToken
	tm.mu.RUnlock()
	return token, nil
}

// refreshAccessToken runs while the caller owns the refresh slot.
func (tm *TokenManager) refreshAccessToken(ctx context.Context) error {
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

	req, err := http.NewRequestWithContext(ctx, "POST", tm.tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		// Request creation errors are not retryable
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := tm.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Network errors are retryable
		return utils.NewRetryableError(err)
	}
	defer resp.Body.Close()

	tokenResp, err := decodeTokenResponse(resp)
	if err != nil {
		if resp.StatusCode >= 500 && resp.StatusCode < 600 || resp.StatusCode == http.StatusTooManyRequests {
			return utils.NewRetryableError(err)
		}
		return err
	}

	// Update tokens with lock
	tm.mu.Lock()
	tm.AccessToken = tokenResp.AccessToken
	if tokenResp.RefreshToken != "" {
		tm.RefreshToken = tokenResp.RefreshToken
	}
	tm.ExpiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	tm.tokensDirty = tm.OnTokenRefresh != nil
	tm.mu.Unlock()
	return nil
}

func (tm *TokenManager) persistTokens() error {
	tm.mu.RLock()
	dirty := tm.tokensDirty
	callback := tm.OnTokenRefresh
	accessToken, refreshToken := tm.AccessToken, tm.RefreshToken
	tm.mu.RUnlock()
	if !dirty || callback == nil {
		return nil
	}
	if err := callback(accessToken, refreshToken); err != nil {
		return fmt.Errorf("failed to save refreshed tokens: %w", err)
	}
	tm.mu.Lock()
	tm.tokensDirty = false
	tm.mu.Unlock()
	return nil
}

func (tm *TokenManager) refreshAccessTokenWithRetry(ctx context.Context) error {
	attempt := 0
	return utils.Retry(ctx, func() error {
		attempt++
		err := tm.refreshAccessToken(ctx)
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
	}, tm.retryOptions)
}

func decodeTokenResponse(resp *http.Response) (tokenResponse, error) {
	const maxResponseSize = 64 << 10
	var token tokenResponse
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		return token, fmt.Errorf("read OAuth response: %w", err)
	}
	if len(body) > maxResponseSize {
		return token, fmt.Errorf("OAuth response exceeds size limit")
	}
	if resp.StatusCode != http.StatusOK {
		var failure struct {
			Message string `json:"message"`
		}
		// Classification uses only the documented message, never raw error text.
		json.Unmarshal(body, &failure)
		return token, &OAuthError{
			StatusCode:          resp.StatusCode,
			InvalidRefreshToken: resp.StatusCode == http.StatusBadRequest && strings.EqualFold(failure.Message, "Invalid refresh token"),
		}
	}
	if err := json.Unmarshal(body, &token); err != nil {
		return token, fmt.Errorf("invalid OAuth token response")
	}
	if strings.TrimSpace(token.AccessToken) == "" || token.ExpiresIn <= 0 || int64(token.ExpiresIn) > int64((1<<63-1)/time.Second) {
		return token, fmt.Errorf("OAuth response has an invalid access token or expiry")
	}
	return token, nil
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

	req, err := http.NewRequestWithContext(ctx, "POST", tokenEndpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: utils.HTTPClientTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	tokenResp, err := decodeTokenResponse(resp)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(tokenResp.RefreshToken) == "" {
		return nil, fmt.Errorf("OAuth response is missing a refresh token")
	}
	tm := NewTokenManager(clientID, clientSecret, tokenResp.AccessToken)
	tm.RefreshToken = tokenResp.RefreshToken
	tm.ExpiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)

	return tm, nil
}
