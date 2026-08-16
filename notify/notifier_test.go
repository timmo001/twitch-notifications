package notify

import (
	"reflect"
	"testing"
)

func TestOmarchyNotificationArgs(t *testing.T) {
	t.Setenv("OMARCHY_HOST", "laptop")

	got := omarchyNotificationArgs(
		"Twitch Notifications",
		"Channel is live",
		"Playing: Game",
		"https://twitch.tv/channel",
	)
	want := []string{
		"notification", "send",
		"-g", omarchyNotificationGlyph,
		"--app-name", "Twitch Notifications",
		"--exec", "omarchy-launch-webapp 'https://twitch.tv/channel'",
		"Channel is live", "Playing: Game",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected Omarchy arguments:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestOmarchyNotificationArgsWithoutActionOrBody(t *testing.T) {
	t.Setenv("OMARCHY_HOST", "")

	got := omarchyNotificationArgs("Twitch Notifications", "Started", "", "")
	want := []string{
		"notification", "send",
		"-g", omarchyNotificationGlyph,
		"--app-name", "Twitch Notifications",
		"Started",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected Omarchy arguments:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestOmarchyOpenCommandForDesktop(t *testing.T) {
	t.Setenv("OMARCHY_HOST", "desktop")

	if got, want := omarchyOpenCommand("https://twitch.tv/channel"), "omarchy-launch-browser 'https://twitch.tv/channel'"; got != want {
		t.Fatalf("omarchyOpenCommand() = %q, want %q", got, want)
	}
}

func TestShellQuote(t *testing.T) {
	t.Parallel()

	if got, want := shellQuote("https://example.com/a'b"), "'https://example.com/a'\\''b'"; got != want {
		t.Fatalf("shellQuote() = %q, want %q", got, want)
	}
}
