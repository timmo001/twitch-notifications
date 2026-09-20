package main

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"twitch-notifications/auth"
	"twitch-notifications/config"
	"twitch-notifications/twitch"
)

func TestOAuthRetryUsesTypedFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"no error", nil, false},
		{"missing tokens", auth.ErrMissingToken, true},
		{"API unauthorised", &twitch.APIError{StatusCode: 401}, true},
		{"OAuth unauthorised", &auth.OAuthError{StatusCode: 401}, true},
		{"expired refresh", &auth.OAuthError{StatusCode: 400, InvalidRefreshToken: true}, true},
		{"invalid client", &auth.OAuthError{StatusCode: 400}, false},
		{"server failure", &auth.OAuthError{StatusCode: 503}, false},
		{"cancelled", context.Canceled, false},
		{"misleading text", errors.New("no valid access token: token refresh failed (status 401)"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldTriggerOAuthRetry(tc.err); got != tc.want {
				t.Fatalf("shouldTriggerOAuthRetry() = %t, want %t", got, tc.want)
			}
			if tc.err != nil {
				if got := shouldTriggerOAuthRetry(fmt.Errorf("wrapped: %w", tc.err)); got != tc.want {
					t.Fatalf("wrapped error classification = %t, want %t", got, tc.want)
				}
			}
		})
	}
}

func boolPointer(value bool) *bool {
	return &value
}

func TestRemovedTUICommandDoesNotStartDaemon(t *testing.T) {
	for _, args := range [][]string{
		{"tui"},
		{"--config", "custom.yaml", "tui"},
	} {
		handled, err := handleCLICommand(args, "config.yaml")
		if !handled || err == nil {
			t.Fatalf("handleCLICommand(%q) = (%t, %v), want handled error", args, handled, err)
		}
	}
}

func TestBuildFollowedLiveChannelsIncludesWatchedChannels(t *testing.T) {
	t.Parallel()

	channels := buildFollowedLiveChannels(
		[]twitch.LiveStream{
			{BroadcasterUserLogin: "watched", StreamTitle: "Configured"},
			{BroadcasterUserLogin: "other", StreamTitle: "Other stream", GameName: "Other game", ThumbnailURL: "https://example.com/other.jpg"},
		},
	)

	want := []statusJSONChannel{
		{Login: "watched", Title: "Configured", Live: true},
		{Login: "other", Title: "Other stream", GameName: "Other game", ThumbnailURL: "https://example.com/other.jpg", Live: true},
	}
	if !reflect.DeepEqual(channels, want) {
		t.Fatalf("buildFollowedLiveChannels() = %#v, want %#v", channels, want)
	}
}

func TestBuildStatusJSONPayload(t *testing.T) {
	t.Parallel()

	payload := buildStatusJSONPayload(
		true,
		2,
		[]statusJSONChannel{
			{Login: "second", Title: "A title: with a colon", GameName: "Second game", ThumbnailURL: "https://example.com/second.jpg", Live: true},
			{Login: "FIRST", Title: "First title", GameName: "First game", ThumbnailURL: "https://example.com/first.jpg", Live: true},
		},
		[]config.WatchedChannel{
			{Name: "first", Open: boolPointer(true)},
			{Name: "second", Open: boolPointer(false)},
			{Name: "offline", Open: nil},
		},
	)

	want := statusJSONPayload{
		Active:    true,
		State:     "live",
		LiveCount: 2,
		Channels: []statusJSONChannel{
			{Login: "first", Title: "First title", GameName: "First game", ThumbnailURL: "https://example.com/first.jpg", Live: true, AutoOpen: true},
			{Login: "second", Title: "A title: with a colon", GameName: "Second game", ThumbnailURL: "https://example.com/second.jpg", Live: true, AutoOpen: false},
			{Login: "offline", Title: "", Live: false, AutoOpen: false},
		},
	}

	if !reflect.DeepEqual(payload, want) {
		t.Fatalf("buildStatusJSONPayload() = %#v, want %#v", payload, want)
	}
}

func TestBuildStatusJSONPayloadStates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		active    bool
		liveCount int
		want      string
	}{
		{name: "inactive", active: false, liveCount: 0, want: "inactive"},
		{name: "active", active: true, liveCount: 0, want: "active"},
		{name: "live", active: true, liveCount: 1, want: "live"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			payload := buildStatusJSONPayload(test.active, test.liveCount, nil, nil)
			if payload.State != test.want {
				t.Fatalf("State = %q, want %q", payload.State, test.want)
			}
		})
	}
}
