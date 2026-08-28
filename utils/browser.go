package utils

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
)

// Opener handles opening URLs in the browser
type Opener struct{}

// NewOpener creates a new browser opener
func NewOpener() *Opener {
	return &Opener{}
}

// OpenURL opens a URL in the default browser in the background
func (o *Opener) OpenURL(url string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	default: // Linux and others
		cmd = openCommand(url)
	}

	return o.openURL(url, cmd)
}

// OpenAppURL opens a URL in a standalone browser window when supported.
func (o *Opener) OpenAppURL(url string) error {
	if runtime.GOOS != "darwin" {
		if _, err := exec.LookPath("omarchy-launch-webapp"); err == nil {
			return o.openURL(url, exec.Command("omarchy-launch-webapp", url))
		}
	}

	return o.OpenURL(url)
}

func (o *Opener) openURL(url string, cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to open URL: %w", err)
	}

	// Don't wait for the process, let it run in background
	go func() {
		if err := cmd.Wait(); err != nil {
			log.Printf("Browser process error (non-fatal): %v", err)
		}
	}()

	log.Printf("Opened URL in browser: %s", url)
	return nil
}

func openCommand(url string) *exec.Cmd {
	switch os.Getenv("OMARCHY_HOST") {
	case "desktop":
		// Desktop: skip webapp, use normal browser
		if _, err := exec.LookPath("omarchy-launch-browser"); err == nil {
			return exec.Command("omarchy-launch-browser", url)
		}
	case "laptop":
		// Laptop: use webapp variant
		if _, err := exec.LookPath("omarchy-launch-webapp"); err == nil {
			return exec.Command("omarchy-launch-webapp", url)
		}
	}

	return exec.Command("xdg-open", url)
}

// OpenTwitchStream opens a Twitch stream URL
func (o *Opener) OpenTwitchStream(channelName string) error {
	url := TwitchStreamURL(channelName)
	return o.OpenURL(url)
}
