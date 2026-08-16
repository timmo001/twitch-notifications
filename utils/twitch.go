package utils

import "fmt"

// TwitchStreamURL generates a Twitch stream URL for a given channel name
func TwitchStreamURL(channelName string) string {
	return fmt.Sprintf("https://www.twitch.tv/%s", channelName)
}
