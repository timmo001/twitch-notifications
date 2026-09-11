package tray

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"twitch-notifications/config"

	"fyne.io/systray"
)

func fakeDialog(t *testing.T, exitCode int) {
	t.Helper()
	dir := t.TempDir()
	if exitCode >= 0 {
		script := fmt.Sprintf("#!/bin/sh\nexit %d\n", exitCode)
		if err := os.WriteFile(filepath.Join(dir, "zenity"), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

func TestTrayConfirmation(t *testing.T) {
	for _, tc := range []struct {
		name       string
		quit       bool
		exitCode   int
		saveFails  bool
		wantHidden bool
	}{
		{name: "hide confirmed", exitCode: 0, wantHidden: true},
		{name: "save failure", exitCode: 0, saveFails: true},
		{name: "hide cancelled", exitCode: 1},
		{name: "hide dialog failure", exitCode: 2},
		{name: "hide missing dialog", exitCode: -1},
		{name: "quit cancelled", quit: true, exitCode: 1},
		{name: "quit dialog failure", quit: true, exitCode: 2},
		{name: "quit missing dialog", quit: true, exitCode: -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeDialog(t, tc.exitCode)
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte("system_tray: true\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := config.Load(path); err != nil {
				t.Fatal(err)
			}
			if tc.saveFails {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			ready = false
			stopped = false
			done = make(chan struct{})
			unused := &systray.MenuItem{ClickedCh: make(chan struct{})}
			hide := &systray.MenuItem{ClickedCh: make(chan struct{})}
			quit := &systray.MenuItem{ClickedCh: make(chan struct{})}
			launch := &systray.MenuItem{ClickedCh: make(chan struct{})}
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				handleMenuClicks(unused, unused, unused, unused, unused, launch, hide, unused, quit)
			}()
			t.Cleanup(func() {
				Quit()
				<-finished
			})
			if tc.quit {
				quit.ClickedCh <- struct{}{}
			} else {
				hide.ClickedCh <- struct{}{}
			}
			if !tc.wantHidden {
				// Receiving another action proves cancellation or failure leaves the tray usable.
				select {
				case launch.ClickedCh <- struct{}{}:
				case <-time.After(time.Second):
					t.Fatal("tray stopped handling actions after cancellation or failure")
				}
				select {
				case <-done:
					t.Fatal("tray stopped despite cancellation or failure")
				default:
				}
				if tc.saveFails {
					return
				}
			} else {
				select {
				case <-finished:
				case <-time.After(time.Second):
					t.Fatal("hide did not stop the tray menu")
				}
			}
			cfg, err := config.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.ShouldShowSystemTray() == tc.wantHidden {
				t.Fatalf("tray visibility = %t, want %t", cfg.ShouldShowSystemTray(), !tc.wantHidden)
			}
		})
	}
}
