package utils

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func TestDBusServiceSurvivesTrayShutdown(t *testing.T) {
	if os.Getenv("TWITCH_NOTIFICATIONS_TEST_BUS") != "1" {
		launcher, err := exec.LookPath("dbus-run-session")
		if err != nil {
			t.Skip("dbus-run-session is required for an isolated session bus")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, launcher, "--", os.Args[0], "-test.run=^TestDBusServiceSurvivesTrayShutdown$", "-test.v")
		cmd.Env = append(os.Environ(), "TWITCH_NOTIFICATIONS_TEST_BUS=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated DBus test failed: %v\n%s", err, output)
		}
		return
	}

	trayBus, err := dbus.SessionBus()
	if err != nil {
		t.Fatal(err)
	}
	defer trayBus.Close()
	if _, err := NewDBusService(); err != nil {
		t.Fatal(err)
	}
	defer CloseDBusService()

	// systray closes this shared connection when its icon is hidden.
	if err := trayBus.Close(); err != nil {
		t.Fatal(err)
	}
	client, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var response string
	err = client.Object(dbusServiceName, dbus.ObjectPath(dbusObjectPath)).CallWithContext(ctx, dbusInterface+".Ping", 0).Store(&response)
	if err != nil {
		t.Fatalf("daemon IPC disconnected when the tray closed: %v", err)
	}
	if response != "pong" {
		t.Fatalf("Ping() = %q, want pong", response)
	}
}
