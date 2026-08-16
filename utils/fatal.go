package utils

import (
	"fmt"
	"log"
	"os"
	"sync"
)

// Notifier interface for fatal error notifications
type Notifier interface {
	NotifyFatalError(errorMsg string) error
}

// ClosableNotifier is a notifier that can be closed
type ClosableNotifier interface {
	Notifier
	Close() error
}

var (
	globalNotifier Notifier
	notifierMu     sync.RWMutex
)

// SetGlobalNotifier sets the global notifier for fatal error handling
func SetGlobalNotifier(notifier Notifier) {
	notifierMu.Lock()
	defer notifierMu.Unlock()
	globalNotifier = notifier
}

// CloseAndSetGlobalNotifier closes the previous notifier (if it's closable) and sets a new one
func CloseAndSetGlobalNotifier(notifier Notifier) {
	notifierMu.Lock()
	defer notifierMu.Unlock()

	// Close previous notifier if it's closable
	if globalNotifier != nil {
		if closable, ok := globalNotifier.(ClosableNotifier); ok {
			if err := closable.Close(); err != nil {
				log.Printf("Failed to close previous notifier: %v", err)
			}
		}
	}

	globalNotifier = notifier
}

// CloseGlobalNotifier closes the global notifier if it's closable
func CloseGlobalNotifier() {
	notifierMu.Lock()
	defer notifierMu.Unlock()

	if globalNotifier != nil {
		if closable, ok := globalNotifier.(ClosableNotifier); ok {
			if err := closable.Close(); err != nil {
				log.Printf("Failed to close global notifier: %v", err)
			}
		}
		globalNotifier = nil
	}
}

// FatalError logs the error, sends a notification, and exits the program
// This should be used for any fatal/non-recoverable errors
func FatalError(format string, args ...interface{}) {
	errorMsg := fmt.Sprintf(format, args...)

	// Log the error
	log.Printf("FATAL: %s", errorMsg)

	// Send notification if notifier is available
	notifierMu.RLock()
	notifier := globalNotifier
	notifierMu.RUnlock()

	if notifier != nil {
		if err := notifier.NotifyFatalError(errorMsg); err != nil {
			log.Printf("Failed to send fatal error notification: %v", err)
		}
	}

	// Exit with error code
	os.Exit(1)
}

// FatalErrorWithCode logs the error, sends a notification, and exits with specific code
func FatalErrorWithCode(code int, format string, args ...interface{}) {
	errorMsg := fmt.Sprintf(format, args...)

	// Log the error
	log.Printf("FATAL: %s", errorMsg)

	// Send notification if notifier is available
	notifierMu.RLock()
	notifier := globalNotifier
	notifierMu.RUnlock()

	if notifier != nil {
		if err := notifier.NotifyFatalError(errorMsg); err != nil {
			log.Printf("Failed to send fatal error notification: %v", err)
		}
	}

	// Exit with specified error code
	os.Exit(code)
}

// FatalRestart logs the error and restarts the application instead of exiting.
// Use this for errors that may be transient and could resolve after a restart
// (e.g. network issues, API errors, runtime panics). For truly unrecoverable
// errors (e.g. missing config), use FatalError instead.
func FatalRestart(format string, args ...interface{}) {
	errorMsg := fmt.Sprintf(format, args...)

	// Log the error
	log.Printf("FATAL (restarting): %s", errorMsg)

	// Close global notifier before restarting to release resources
	CloseGlobalNotifier()

	// Close DBus service before restarting to release the name
	CloseDBusService()

	// Restart the application
	RestartSelf(false)

	// If RestartSelf fails to spawn a new process, exit anyway
	os.Exit(1)
}

// GoWithRecovery launches a goroutine with panic recovery.
// If the goroutine panics, the application is silently restarted.
// Use this for background goroutines where a panic should not go unhandled.
func GoWithRecovery(name string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				FatalRestart("panic in goroutine %q: %v", name, r)
			}
		}()
		fn()
	}()
}
