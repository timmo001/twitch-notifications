package tray

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfirmationArguments(t *testing.T) {
	dir := t.TempDir()
	argsPath := filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CONFIRMATION_ARGS\"\n"
	if err := os.WriteFile(filepath.Join(dir, "zenity"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("CONFIRMATION_ARGS", argsPath)
	if !confirmAction(context.Background(), "Hide system tray", "Keep <notifications> running?") {
		t.Fatal("confirmation was not accepted")
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, arg := range []string{"--question", "--no-markup", "--default-cancel", "--title=Hide system tray", "--text=Keep <notifications> running?", "--ok-label=Confirm", "--cancel-label=Cancel"} {
		if !strings.Contains(string(args), arg+"\n") {
			t.Errorf("missing dialog argument %q", arg)
		}
	}
}

func TestCancelledConfirmation(t *testing.T) {
	fakeDialog(t, 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if confirmAction(ctx, "Quit", "Quit Twitch Notifications?") {
		t.Fatal("a cancelled context must not confirm an action")
	}
}
