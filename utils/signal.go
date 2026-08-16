package utils

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	// restartCountEnvVar carries the number of consecutive rapid restarts across
	// process generations. It is read at startup to scale the restart delay and
	// incremented each time RestartSelf spawns a new instance after a quick crash.
	restartCountEnvVar = "TWITCH_NOTIFICATIONS_RESTART_COUNT"

	// supervisedEnvVar disables detached self-restarts so an external supervisor
	// can observe the exit and retain ownership of the replacement process.
	supervisedEnvVar = "TWITCH_NOTIFICATIONS_SUPERVISED"

	// restartBaseDelay is the minimum delay a restarted instance waits before
	// doing work, giving the old process time to release D-Bus names, the tray, etc.
	restartBaseDelay = 5 * time.Second

	// restartMaxDelay caps the exponential backoff so a persistent crash loop
	// settles into retrying every few minutes instead of hammering every 5 seconds.
	restartMaxDelay = 5 * time.Minute

	// restartStabilityWindow is how long an instance must run before a restart is
	// treated as a one-off rather than part of a crash loop. A process that ran at
	// least this long resets the rapid-restart counter back to zero.
	restartStabilityWindow = 90 * time.Second

	// maxRestartCount caps the stored count to keep the backoff calculation well
	// within int64 range (the delay is already capped at restartMaxDelay long
	// before this is reached).
	maxRestartCount = 16
)

// workStartTime holds the unix-nano timestamp at which the current process began
// doing real work. It stays zero if the process crashes before MarkWorkStarted is
// called, which is itself treated as a rapid (early) crash.
var workStartTime atomic.Int64

// MarkWorkStarted records that the process has begun its real work. RestartSelf
// uses this to decide whether a restart follows a stable run (reset the backoff)
// or a rapid crash (escalate the backoff).
func MarkWorkStarted() {
	workStartTime.Store(time.Now().UnixNano())
}

// restartCountFromEnv reads the consecutive rapid-restart count from the
// environment, clamped to a sane range.
func restartCountFromEnv() int {
	count, err := strconv.Atoi(os.Getenv(restartCountEnvVar))
	if err != nil || count < 0 {
		return 0
	}
	if count > maxRestartCount {
		return maxRestartCount
	}
	return count
}

// RestartDelay returns how long a freshly launched instance should wait before
// starting work. It grows exponentially with the number of consecutive rapid
// restarts (read from the environment) and is capped at restartMaxDelay. A normal
// launch with no prior rapid restarts waits restartBaseDelay.
func RestartDelay() time.Duration {
	count := restartCountFromEnv()
	opts := RetryOptions{
		BaseDelay: restartBaseDelay,
		MaxDelay:  restartMaxDelay,
		Jitter:    0.2,
	}
	// CalculateBackoff is 1-based: attempt 1 == base delay, so count 0 keeps the
	// base 5s floor and each additional rapid restart doubles it up to the cap.
	return CalculateBackoff(count+1, opts)
}

// nextRestartCount computes the rapid-restart count to hand to the next instance.
// If the current process ran at least restartStabilityWindow before restarting,
// the count resets to zero; otherwise it increments (capped at maxRestartCount).
func nextRestartCount() int {
	startNanos := workStartTime.Load()
	if startNanos > 0 {
		if uptime := time.Since(time.Unix(0, startNanos)); uptime >= restartStabilityWindow {
			return 0
		}
	}

	next := restartCountFromEnv() + 1
	if next > maxRestartCount {
		return maxRestartCount
	}
	return next
}

// childEnvWithRestartCount returns the parent environment with the restart-count
// variable set to count, replacing any existing value so the child reads a single
// authoritative number.
func childEnvWithRestartCount(count int) []string {
	prefix := restartCountEnvVar + "="
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		if len(kv) >= len(prefix) && kv[:len(prefix)] == prefix {
			continue
		}
		env = append(env, kv)
	}
	return append(env, fmt.Sprintf("%s%d", prefix, count))
}

// SendShutdownSignal sends a SIGTERM signal to the current process for graceful shutdown
func SendShutdownSignal() error {
	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		return err
	}
	if p != nil {
		return p.Signal(syscall.SIGTERM)
	}
	return nil
}

// RestartSelf spawns a new instance of the application with a startup delay flag,
// then returns so the caller can exit. The new process waits for the delay before
// doing any work, giving the old process time to fully release resources (D-Bus names,
// system tray, etc.). This works without a process manager (e.g. Hyprland autostart).
//
// To avoid a tight relaunch loop when an instance keeps crashing, the new process
// inherits a rapid-restart counter (restartCountEnvVar). The counter escalates the
// startup delay exponentially (capped at restartMaxDelay) and resets once an
// instance has run stably for restartStabilityWindow.
func RestartSelf(openBrowser bool) {
	if os.Getenv(supervisedEnvVar) == "1" {
		log.Println("Supervised process requested a restart; exiting for the supervisor")
		os.Exit(1)
	}

	exePath, err := os.Executable()
	if err != nil {
		log.Printf("Failed to get executable path for restart: %v", err)
		return
	}

	// Build args: pass through original args but ensure -delay and -silent are included.
	// Auto-open is one-shot, so only carry it when this restart explicitly requests it.
	args := make([]string, 0, len(os.Args)+3)
	hasDelay := false
	hasSilent := false
	for _, arg := range os.Args {
		if arg == "-open" || arg == "--open" {
			continue
		}
		if openBrowser && (arg == "-silent" || arg == "--silent") {
			continue
		}
		args = append(args, arg)
		if arg == "-delay" || arg == "--delay" {
			hasDelay = true
		}
		if arg == "-silent" || arg == "--silent" {
			hasSilent = true
		}
	}
	if !hasDelay {
		args = append(args, "-delay")
	}
	if !hasSilent && !openBrowser {
		args = append(args, "-silent")
	}
	if openBrowser {
		args = append(args, "-open")
	}

	count := nextRestartCount()
	if count > 0 {
		log.Printf("Rapid restart #%d detected; the new instance will back off before starting", count)
	}

	log.Printf("Spawning new instance: %s %v", exePath, args[1:])

	cmd := exec.Command(exePath, args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = childEnvWithRestartCount(count)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, // Detach from parent process group
	}

	if err := cmd.Start(); err != nil {
		log.Printf("Failed to restart: %v", err)
		return
	}

	log.Printf("New instance started (PID %d), exiting old instance...", cmd.Process.Pid)

	// Force exit to ensure the old process doesn't linger.
	// All cleanup should already be done by the caller before RestartSelf is called.
	// Without this, native threads (e.g. GTK systray) can keep the process alive
	// even after main() returns.
	os.Exit(0)
}
