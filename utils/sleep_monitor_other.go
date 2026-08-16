//go:build !linux
// +build !linux

package utils

// SleepMonitor monitors system sleep/resume events and calls the callback on resume
// On non-Linux platforms, this is a no-op
func SleepMonitor(onResume func()) error {
	// Not implemented on non-Linux platforms
	return nil
}
