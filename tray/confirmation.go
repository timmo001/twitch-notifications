package tray

import (
	"context"
	"errors"
	"log"
	"os/exec"
)

func confirmAction(ctx context.Context, title, message string) bool {
	cmd := exec.CommandContext(ctx, "zenity", "--question", "--no-markup", "--default-cancel", "--title="+title, "--text="+message, "--ok-label=Confirm", "--cancel-label=Cancel")
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if ctx.Err() == nil && !(errors.As(err, &exitErr) && exitErr.ExitCode() == 1) {
			log.Printf("Failed to show %q confirmation: %v", title, err)
		}
		return false
	}
	return ctx.Err() == nil
}
