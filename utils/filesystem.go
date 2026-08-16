package utils

import (
	"log"
	"os"
	"os/exec"
	"runtime"
)

// OpenFile opens a file with the default application (cross-platform)
func OpenFile(path string) error {
	// Check if file exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return err
	}

	var cmd *exec.Cmd
	var editorUsed string
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
		editorUsed = "open"
	default: // Linux and others
		// Try to use a text editor for config files
		// First check $EDITOR environment variable
		if editor := os.Getenv("EDITOR"); editor != "" {
			cmd = exec.Command(editor, path)
			editorUsed = editor
		} else {
			// Try common GUI text editors first (in order of preference)
			// These work well when launched from a system tray
			guiEditors := []string{"code", "gedit", "kate", "mousepad", "pluma", "leafpad"}
			var foundEditor string
			for _, editor := range guiEditors {
				if _, err := exec.LookPath(editor); err == nil {
					foundEditor = editor
					break
				}
			}
			// If no GUI editor found, try terminal editors as fallback
			if foundEditor == "" {
				terminalEditors := []string{"nano", "vim", "vi"}
				for _, editor := range terminalEditors {
					if _, err := exec.LookPath(editor); err == nil {
						foundEditor = editor
						break
					}
				}
			}
			if foundEditor != "" {
				cmd = exec.Command(foundEditor, path)
				editorUsed = foundEditor
			} else {
				// Fall back to xdg-open
				cmd = exec.Command("xdg-open", path)
				editorUsed = "xdg-open"
			}
		}
	}

	log.Printf("Opening file using %s: %s", editorUsed, path)

	// For GUI applications, use Start() instead of Run() to avoid blocking
	// Most GUI editors fork and return immediately anyway
	if runtime.GOOS != "darwin" && editorUsed != "xdg-open" {
		// GUI editors should be started (not run) to avoid blocking
		// Terminal editors will still block, but that's expected
		if editorUsed == "code" || editorUsed == "gedit" || editorUsed == "kate" ||
			editorUsed == "mousepad" || editorUsed == "pluma" || editorUsed == "leafpad" {
			return cmd.Start()
		}
	}

	return cmd.Run()
}
