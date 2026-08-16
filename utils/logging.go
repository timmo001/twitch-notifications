package utils

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	logDateLayout = "2006-01-02"
	logRetention  = 7 * 24 * time.Hour
)

type dailyLogWriter struct {
	mu              sync.Mutex
	dir             string
	currentDate     string
	file            *os.File
	lastCleanupDate string
}

// SetupLogging writes logs to ~/.local/state/<appDirName>/<date>.log while
// keeping stderr output for interactive runs.
func SetupLogging(appDirName string) (io.Closer, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to determine home directory: %w", err)
	}

	writer := &dailyLogWriter{
		dir: filepath.Join(homeDir, ".local", "state", appDirName),
	}

	if err := writer.rotateLocked(time.Now()); err != nil {
		return nil, err
	}

	log.SetOutput(io.MultiWriter(os.Stderr, writer))
	return writer, nil
}

func (w *dailyLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.rotateLocked(time.Now()); err != nil {
		return 0, err
	}

	return w.file.Write(p)
}

func (w *dailyLogWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return nil
	}

	err := w.file.Close()
	w.file = nil
	return err
}

func (w *dailyLogWriter) rotateLocked(now time.Time) error {
	if err := os.MkdirAll(w.dir, 0755); err != nil {
		return fmt.Errorf("failed to create log directory %s: %w", w.dir, err)
	}

	date := now.Format(logDateLayout)
	if w.file != nil && w.currentDate == date {
		w.cleanupOldLogsLocked(now)
		return nil
	}

	if w.file != nil {
		if err := w.file.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close log file: %v\n", err)
		}
	}

	logPath := filepath.Join(w.dir, date+".log")
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		w.file = nil
		w.currentDate = ""
		return fmt.Errorf("failed to open log file %s: %w", logPath, err)
	}

	w.file = file
	w.currentDate = date
	w.cleanupOldLogsLocked(now)
	return nil
}

func (w *dailyLogWriter) cleanupOldLogsLocked(now time.Time) {
	today := now.Format(logDateLayout)
	if w.lastCleanupDate == today {
		return
	}
	defer func() {
		w.lastCleanupDate = today
	}()

	entries, err := os.ReadDir(w.dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to read log directory %s: %v\n", w.dir, err)
		return
	}

	cutoff := now.Add(-logRetention)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".log") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to inspect log file %s: %v\n", entry.Name(), err)
			continue
		}

		if !info.ModTime().Before(cutoff) {
			continue
		}

		logPath := filepath.Join(w.dir, entry.Name())
		if err := os.Remove(logPath); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Warning: failed to remove old log file %s: %v\n", logPath, err)
		}
	}
}
