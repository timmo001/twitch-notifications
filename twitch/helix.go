package twitch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"twitch-notifications/utils"

	"github.com/nicklaw5/helix"
)

const (
	// maxPerRequest is the maximum number of items per Twitch API request
	maxPerRequest = 100

	// API request timing
	batchRequestDelay = 500 * time.Millisecond // Delay between batch requests

	// Rate limit timing
	rateLimitWindow     = 65 * time.Second // Full 60s window + 5s buffer
	rateLimitMaxWait    = 2 * time.Minute  // Maximum wait time
	rateLimitResetCheck = 60 * time.Second // Check window for reset
)

// RateLimitInfo contains rate limit information from Twitch API responses
type RateLimitInfo struct {
	Limit     int       // Total points in bucket
	Remaining int       // Remaining points in bucket
	Reset     time.Time // When the bucket resets
}

// RateLimitResponse wraps an HTTP response with rate limit information
type RateLimitResponse struct {
	StatusCode int
	RateLimit  *RateLimitInfo
	RetryAfter time.Duration // Retry-After header value (for 429 responses)
	Body       []byte
}

// HelixClient wraps the helix API client
type HelixClient struct {
	client      *helix.Client
	clientID    string
	accessToken string
	rateLimitMu sync.RWMutex
	rateLimit   *RateLimitInfo // Track latest rate limit state
}

// NewHelixClient creates a new Helix API client
func NewHelixClient(clientID, accessToken string) (*HelixClient, error) {
	client, err := helix.NewClient(&helix.Options{
		ClientID:        clientID,
		UserAccessToken: accessToken,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create helix client: %w", err)
	}

	return &HelixClient{
		client:      client,
		clientID:    clientID,
		accessToken: accessToken,
	}, nil
}

// UpdateAccessToken updates the access token for the client
func (hc *HelixClient) UpdateAccessToken(accessToken string) {
	hc.client.SetUserAccessToken(accessToken)
	hc.accessToken = accessToken
}

// GetUserID fetches the user ID for the authenticated user
// Uses the latest Get Users endpoint
func (hc *HelixClient) GetUserID(ctx context.Context) (string, error) {
	resp, err := hc.client.GetUsers(&helix.UsersParams{})
	if err != nil {
		return "", fmt.Errorf("failed to get user info: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", NewAPIError(resp.StatusCode, resp.ErrorMessage, nil)
	}

	if len(resp.Data.Users) == 0 {
		return "", fmt.Errorf("no user data returned")
	}

	return resp.Data.Users[0].ID, nil
}

// Channel represents a Twitch channel
type Channel struct {
	ID       string
	Username string
}

// GetChannelsByUsernames fetches channel information (ID) from a list of usernames/logins
// The returned channels preserve the order of the input usernames slice.
// This is important because the first N channels get EventSub while the rest use polling.
func (hc *HelixClient) GetChannelsByUsernames(ctx context.Context, usernames []string) ([]Channel, error) {
	if len(usernames) == 0 {
		return []Channel{}, nil
	}

	// Use a map to collect results - the Twitch API does not guarantee response order
	channelMap := make(map[string]Channel)

	for i := 0; i < len(usernames); i += maxPerRequest {
		end := i + maxPerRequest
		if end > len(usernames) {
			end = len(usernames)
		}
		batch := usernames[i:end]

		// Use helix library's GetUsers with logins
		params := &helix.UsersParams{}
		for _, username := range batch {
			params.Logins = append(params.Logins, username)
		}

		resp, err := hc.client.GetUsers(params)
		if err != nil {
			return nil, fmt.Errorf("failed to get users: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			return nil, NewAPIError(resp.StatusCode, resp.ErrorMessage, nil)
		}

		// Store results in map with lowercase username as key for case-insensitive lookup
		for _, user := range resp.Data.Users {
			channelMap[strings.ToLower(user.Login)] = Channel{
				ID:       user.ID,
				Username: user.Login,
			}
		}

		// Rate limit: wait between batches (increased delay to avoid rate limits)
		if end < len(usernames) {
			time.Sleep(batchRequestDelay)
		}
	}

	// Rebuild the channel list preserving the original input order
	allChannels := make([]Channel, 0, len(usernames))
	for _, username := range usernames {
		if ch, ok := channelMap[strings.ToLower(username)]; ok {
			allChannels = append(allChannels, ch)
		}
		// If channel not found (invalid username), it's silently skipped
	}

	return allChannels, nil
}

// parseRateLimitHeaders parses rate limit headers from HTTP response
func (hc *HelixClient) parseRateLimitHeaders(resp *http.Response) *RateLimitInfo {
	limitHeader := resp.Header.Get("Ratelimit-Limit")
	remainingHeader := resp.Header.Get("Ratelimit-Remaining")
	resetHeader := resp.Header.Get("Ratelimit-Reset")

	if limitHeader == "" && remainingHeader == "" && resetHeader == "" {
		return nil
	}

	rateLimit := &RateLimitInfo{}

	if limitHeader != "" {
		if limit, err := strconv.Atoi(limitHeader); err == nil {
			rateLimit.Limit = limit
		}
	}

	if remainingHeader != "" {
		if remaining, err := strconv.Atoi(remainingHeader); err == nil {
			rateLimit.Remaining = remaining
		}
	}

	if resetHeader != "" {
		if resetUnix, err := strconv.ParseInt(resetHeader, 10, 64); err == nil {
			rateLimit.Reset = time.Unix(resetUnix, 0)
		}
	}

	// Update cached rate limit state
	hc.rateLimitMu.Lock()
	hc.rateLimit = rateLimit
	hc.rateLimitMu.Unlock()

	return rateLimit
}

// parseRetryAfter parses the Retry-After header from HTTP response
func parseRetryAfter(resp *http.Response) time.Duration {
	retryAfterHeader := resp.Header.Get("Retry-After")
	if retryAfterHeader == "" {
		return 0
	}

	if seconds, err := strconv.Atoi(retryAfterHeader); err == nil {
		return time.Duration(seconds) * time.Second
	}

	return 0
}

// GetRateLimit returns the current cached rate limit information
func (hc *HelixClient) GetRateLimit() *RateLimitInfo {
	hc.rateLimitMu.RLock()
	defer hc.rateLimitMu.RUnlock()
	if hc.rateLimit == nil {
		return nil
	}
	// Return a copy to avoid race conditions
	return &RateLimitInfo{
		Limit:     hc.rateLimit.Limit,
		Remaining: hc.rateLimit.Remaining,
		Reset:     hc.rateLimit.Reset,
	}
}

// calculateDelay calculates the appropriate delay based on rate limit information
func (hc *HelixClient) calculateDelay(rateLimit *RateLimitInfo) time.Duration {
	const (
		baseDelay       = 800 * time.Millisecond // Increased base delay to avoid rate limits
		minDelay        = 200 * time.Millisecond
		maxDelay        = 5 * time.Second
		lowQuotaPercent = 0.2 // 20% remaining is considered low (more aggressive)
	)

	if rateLimit == nil {
		return baseDelay
	}

	now := time.Now()

	// If we have a reset time and we're close to it, calculate wait time
	if !rateLimit.Reset.IsZero() && rateLimit.Remaining <= 0 {
		waitTime := rateLimit.Reset.Sub(now)
		if waitTime > 0 {
			// Add a small buffer
			return waitTime + 100*time.Millisecond
		}
	}

	// If remaining quota is low, increase delay - more aggressive scaling
	if rateLimit.Limit > 0 {
		remainingPercent := float64(rateLimit.Remaining) / float64(rateLimit.Limit)
		if remainingPercent < lowQuotaPercent {
			// Scale delay more aggressively when quota is low
			delayMultiplier := 1.0 / (remainingPercent + 0.05)
			delay := baseDelay * time.Duration(delayMultiplier)
			if delay > maxDelay {
				return maxDelay
			}
			if delay < minDelay {
				return minDelay
			}
			return delay
		} else if remainingPercent < 0.5 {
			// Medium-low quota - use slightly increased delay
			return baseDelay * 3 / 2
		}
	}

	// Normal operation - use base delay
	return baseDelay
}

// RateLimitWaitResult contains information about a rate limit wait operation
type RateLimitWaitResult struct {
	Waited       bool          // Whether we actually waited
	WaitDuration time.Duration // How long we waited
	WasExhausted bool          // Whether quota was exhausted (remaining <= 0)
}

// WaitForRateLimit checks rate limits and waits if necessary before making an API request.
// This is the single source of truth for rate limit handling across all API calls.
// Returns information about the wait and any context cancellation error.
func (hc *HelixClient) WaitForRateLimit(ctx context.Context) (*RateLimitWaitResult, error) {
	return hc.WaitForRateLimitWithBuffer(ctx, 0)
}

// WaitForRateLimitWithBuffer checks rate limits and waits if necessary, with additional buffer time.
// The buffer is added to any wait time to provide extra safety margin.
func (hc *HelixClient) WaitForRateLimitWithBuffer(ctx context.Context, buffer time.Duration) (*RateLimitWaitResult, error) {
	result := &RateLimitWaitResult{}
	rateLimit := hc.GetRateLimit()
	if rateLimit == nil {
		return result, nil
	}

	now := time.Now()

	// If quota is exhausted, wait for full rate limit window
	if rateLimit.Remaining <= 0 {
		result.WasExhausted = true
		var waitTime time.Duration

		if !rateLimit.Reset.IsZero() {
			waitTime = rateLimit.Reset.Sub(now)
		}

		// Use a minimum wait of 65 seconds (full 60s window + 5s buffer)
		if waitTime < rateLimitWindow {
			waitTime = rateLimitWindow
		}

		// Cap at maximum wait to prevent excessive delays
		if waitTime > rateLimitMaxWait {
			waitTime = rateLimitMaxWait
		}

		waitTime += buffer
		result.Waited = true
		result.WaitDuration = waitTime

		select {
		case <-time.After(waitTime):
			return result, nil
		case <-ctx.Done():
			return result, ctx.Err()
		}
	}

	// Proactive slowdown when quota is getting low
	if rateLimit.Limit > 0 {
		remainingPercent := float64(rateLimit.Remaining) / float64(rateLimit.Limit)

		// Critical: less than 10% remaining
		if remainingPercent < 0.1 && !rateLimit.Reset.IsZero() {
			timeUntilReset := time.Until(rateLimit.Reset)
			if timeUntilReset > 0 && timeUntilReset < rateLimitResetCheck {
				waitTime := timeUntilReset + 5*time.Second + buffer
				result.Waited = true
				result.WaitDuration = waitTime

				select {
				case <-time.After(waitTime):
					return result, nil
				case <-ctx.Done():
					return result, ctx.Err()
				}
			}
		}

		// Low: less than 20% remaining - wait until reset if it's coming soon
		if remainingPercent < 0.2 && !rateLimit.Reset.IsZero() {
			timeUntilReset := time.Until(rateLimit.Reset)
			if timeUntilReset > 0 && timeUntilReset < 30*time.Second {
				waitTime := timeUntilReset + 3*time.Second + buffer
				result.Waited = true
				result.WaitDuration = waitTime

				select {
				case <-time.After(waitTime):
					return result, nil
				case <-ctx.Done():
					return result, ctx.Err()
				}
			}
		}

		// Medium-low: less than 40% remaining - add proportional delay
		if remainingPercent < 0.4 {
			extraWait := time.Duration((0.4-remainingPercent)*15) * time.Second
			if extraWait > 8*time.Second {
				extraWait = 8 * time.Second
			}
			if extraWait > 1*time.Second {
				waitTime := extraWait + buffer
				result.Waited = true
				result.WaitDuration = waitTime

				select {
				case <-time.After(waitTime):
					return result, nil
				case <-ctx.Done():
					return result, ctx.Err()
				}
			}
		}
	}

	return result, nil
}

// CreateEventSubSubscription creates an EventSub subscription via Helix API
// Uses direct HTTP call with proper context support and latest API format
// Returns rate limit response with status code, rate limit info, and error
func (hc *HelixClient) CreateEventSubSubscription(ctx context.Context, sessionID, broadcasterUserID string) (*RateLimitResponse, error) {
	subscriptionData := map[string]interface{}{
		"type":    "stream.online",
		"version": "1", // stream.online is version 1 as of December 2025
		"condition": map[string]string{
			"broadcaster_user_id": broadcasterUserID,
		},
		"transport": map[string]string{
			"method":     "websocket",
			"session_id": sessionID,
		},
	}

	jsonData, err := json.Marshal(subscriptionData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal subscription data: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.twitch.tv/helix/eventsub/subscriptions", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Client-ID", hc.clientID)
	req.Header.Set("Authorization", "Bearer "+hc.accessToken)

	client := &http.Client{Timeout: utils.HTTPClientTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Parse rate limit headers
	rateLimit := hc.parseRateLimitHeaders(resp)

	// Read response body
	body, _ := io.ReadAll(resp.Body)

	response := &RateLimitResponse{
		StatusCode: resp.StatusCode,
		RateLimit:  rateLimit,
		RetryAfter: parseRetryAfter(resp),
		Body:       body,
	}

	if resp.StatusCode != http.StatusAccepted {
		return response, NewAPIError(resp.StatusCode, http.StatusText(resp.StatusCode), body)
	}

	return response, nil
}

// LiveStream represents a currently live stream
type LiveStream struct {
	BroadcasterUserID    string
	BroadcasterUserLogin string
	BroadcasterUserName  string
	StreamTitle          string
	GameName             string
	ThumbnailURL         string
	StartedAt            time.Time
}

func streamThumbnailURL(value string) string {
	value = strings.ReplaceAll(value, "{width}", "320")
	return strings.ReplaceAll(value, "{height}", "180")
}

// GetFollowedLiveStreams returns all live streams followed by the authenticated user.
func (hc *HelixClient) GetFollowedLiveStreams(ctx context.Context, userID string) ([]LiveStream, error) {
	streams := make([]LiveStream, 0)
	after := ""

	for {
		if _, err := hc.WaitForRateLimit(ctx); err != nil {
			return nil, err
		}

		params := url.Values{}
		params.Set("user_id", userID)
		params.Set("first", "100")
		if after != "" {
			params.Set("after", after)
		}

		req, err := http.NewRequestWithContext(ctx, "GET", "https://api.twitch.tv/helix/streams/followed?"+params.Encode(), nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create followed streams request: %w", err)
		}
		req.Header.Set("Client-ID", hc.clientID)
		req.Header.Set("Authorization", "Bearer "+hc.accessToken)

		resp, err := (&http.Client{Timeout: utils.HTTPClientTimeout}).Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to get followed streams: %w", err)
		}

		var result struct {
			Data []struct {
				UserID       string `json:"user_id"`
				UserLogin    string `json:"user_login"`
				UserName     string `json:"user_name"`
				Title        string `json:"title"`
				GameName     string `json:"game_name"`
				ThumbnailURL string `json:"thumbnail_url"`
				StartedAt    string `json:"started_at"`
			} `json:"data"`
			Pagination struct {
				Cursor string `json:"cursor"`
			} `json:"pagination"`
		}

		hc.parseRateLimitHeaders(resp)
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, NewAPIError(resp.StatusCode, http.StatusText(resp.StatusCode), body)
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("failed to decode followed streams response: %w", err)
		}
		resp.Body.Close()

		for _, stream := range result.Data {
			startedAt, _ := time.Parse(time.RFC3339, stream.StartedAt)
			streams = append(streams, LiveStream{
				BroadcasterUserID:    stream.UserID,
				BroadcasterUserLogin: stream.UserLogin,
				BroadcasterUserName:  stream.UserName,
				StreamTitle:          stream.Title,
				GameName:             stream.GameName,
				ThumbnailURL:         streamThumbnailURL(stream.ThumbnailURL),
				StartedAt:            startedAt,
			})
		}

		after = result.Pagination.Cursor
		if after == "" {
			return streams, nil
		}
	}
}

// GetLiveStreams checks which of the given channel IDs are currently live
// Returns a map of broadcaster_user_id -> LiveStream
func (hc *HelixClient) GetLiveStreams(ctx context.Context, channelIDs []string) (map[string]LiveStream, error) {
	if len(channelIDs) == 0 {
		return make(map[string]LiveStream), nil
	}

	liveStreams := make(map[string]LiveStream)

	for i := 0; i < len(channelIDs); i += maxPerRequest {
		end := i + maxPerRequest
		if end > len(channelIDs) {
			end = len(channelIDs)
		}
		batch := channelIDs[i:end]

		// Check rate limits before making request
		if _, err := hc.WaitForRateLimit(ctx); err != nil {
			return nil, err
		}

		// Build URL with user_id parameters
		params := url.Values{}
		for _, id := range batch {
			params.Add("user_id", id)
		}
		params.Set("first", "100") // Maximum allowed

		reqURL := "https://api.twitch.tv/helix/streams?" + params.Encode()

		req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("Client-ID", hc.clientID)
		req.Header.Set("Authorization", "Bearer "+hc.accessToken)

		client := &http.Client{Timeout: utils.HTTPClientTimeout}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to get live streams: %w", err)
		}
		defer resp.Body.Close()

		// Parse rate limit headers (before reading body)
		hc.parseRateLimitHeaders(resp)

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return nil, NewAPIError(resp.StatusCode, http.StatusText(resp.StatusCode), body)
		}

		var result struct {
			Data []struct {
				ID           string `json:"id"`
				UserID       string `json:"user_id"`
				UserLogin    string `json:"user_login"`
				UserName     string `json:"user_name"`
				GameID       string `json:"game_id"`
				GameName     string `json:"game_name"`
				Type         string `json:"type"`
				Title        string `json:"title"`
				ViewerCount  int    `json:"viewer_count"`
				StartedAt    string `json:"started_at"`
				Language     string `json:"language"`
				ThumbnailURL string `json:"thumbnail_url"`
			} `json:"data"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return nil, fmt.Errorf("failed to decode response: %w", err)
		}

		for _, stream := range result.Data {
			// Only include live streams (type == "live")
			if stream.Type == "live" {
				startedAt := time.Time{}
				if stream.StartedAt != "" {
					if t, err := time.Parse(time.RFC3339, stream.StartedAt); err == nil {
						startedAt = t
					}
				}

				liveStreams[stream.UserID] = LiveStream{
					BroadcasterUserID:    stream.UserID,
					BroadcasterUserLogin: stream.UserLogin,
					BroadcasterUserName:  stream.UserName,
					StreamTitle:          stream.Title,
					GameName:             stream.GameName,
					ThumbnailURL:         streamThumbnailURL(stream.ThumbnailURL),
					StartedAt:            startedAt,
				}
			}
		}

		// Adjust delay based on rate limit information (after updating from headers)
		currentRateLimit := hc.GetRateLimit()
		delay := hc.calculateDelay(currentRateLimit)

		if end < len(channelIDs) && delay > 0 {
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}

	return liveStreams, nil
}
