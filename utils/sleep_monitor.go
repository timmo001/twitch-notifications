//go:build linux
// +build linux

package utils

import (
	"log"
	"runtime"

	"github.com/godbus/dbus/v5"
)

// SleepMonitor monitors system sleep/resume events and calls the callback on resume
// On Linux, this uses systemd-logind via D-Bus
// On other platforms, this is a no-op
func SleepMonitor(onResume func()) error {
	// Only run on Linux
	if runtime.GOOS != "linux" {
		return nil
	}

	conn, err := dbus.SystemBus()
	if err != nil {
		return err
	}

	// Subscribe to systemd-logind signals
	err = conn.AddMatchSignal(
		dbus.WithMatchInterface("org.freedesktop.login1.Manager"),
		dbus.WithMatchMember("PrepareForSleep"),
	)
	if err != nil {
		return err
	}

	c := make(chan *dbus.Signal, 10)
	conn.Signal(c)

	go func() {
		for signal := range c {
			// PrepareForSleep signal: false means resuming from sleep
			// true means preparing to sleep
			if len(signal.Body) > 0 {
				if preparing, ok := signal.Body[0].(bool); ok {
					if !preparing {
						// System is resuming from sleep
						log.Println("System resumed from sleep, triggering health check...")
						if onResume != nil {
							onResume()
						}
					}
				}
			}
		}
	}()

	return nil
}
