package main

import (
	"reflect"
	"testing"

	"twitch-notifications/config"
	"twitch-notifications/twitch"
)

func boolPointer(value bool) *bool {
	return &value
}

func TestBuildFollowedLiveChannelsExcludesWatchedChannels(t *testing.T) {
	t.Parallel()

	channels := buildFollowedLiveChannels(
		[]twitch.LiveStream{
			{BroadcasterUserLogin: "watched", StreamTitle: "Configured"},
			{BroadcasterUserLogin: "other", StreamTitle: "Other stream", GameName: "Other game", ThumbnailURL: "https://example.com/other.jpg"},
		},
		[]config.WatchedChannel{{Name: "WATCHED"}},
	)

	want := []statusJSONChannel{{Login: "other", Title: "Other stream", GameName: "Other game", ThumbnailURL: "https://example.com/other.jpg", Live: true}}
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
