package utils

import (
	"log"
	"sync"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
)

const (
	// DBus service configuration
	dbusServiceName = "com.github.TwitchNotifications"
	dbusObjectPath  = "/com/github/TwitchNotifications"
	dbusInterface   = "com.github.TwitchNotifications"
)

// DBusService handles DBus IPC for the application
type DBusService struct {
	conn                  *dbus.Conn
	recheckHandler        func(openBrowser bool)
	restartHandler        func(openBrowser bool)
	statusHandler         func() (int, []string)
	detailedStatusHandler func() (int, string)
	mu                    sync.RWMutex
}

// Global DBus service instance
var (
	dbusService   *DBusService
	dbusServiceMu sync.RWMutex
)

// NewDBusService creates and registers a new DBus service
func NewDBusService() (*DBusService, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, err
	}

	service := &DBusService{
		conn: conn,
	}

	// Request the service name
	reply, err := conn.RequestName(dbusServiceName, dbus.NameFlagDoNotQueue)
	if err != nil {
		conn.Close()
		return nil, err
	}

	if reply != dbus.RequestNameReplyPrimaryOwner {
		conn.Close()
		return nil, err
	}

	// Export the service object
	err = conn.Export(service, dbus.ObjectPath(dbusObjectPath), dbusInterface)
	if err != nil {
		conn.Close()
		return nil, err
	}

	// Export introspection data
	introData := introspect.Introspectable(`
<node>
	<interface name="` + dbusInterface + `">
		<method name="Recheck">
			<arg name="open" direction="in" type="b"/>
		</method>
		<method name="Restart"/>
		<method name="RestartOpen"/>
		<method name="GetStatus">
			<arg name="active" direction="out" type="b"/>
			<arg name="live_count" direction="out" type="i"/>
			<arg name="live_channels" direction="out" type="as"/>
		</method>
		<method name="GetDetailedStatus">
			<arg name="active" direction="out" type="b"/>
			<arg name="live_count" direction="out" type="i"/>
			<arg name="live_channels_json" direction="out" type="s"/>
		</method>
		<method name="Ping">
			<arg direction="out" type="s"/>
		</method>
	</interface>
</node>`)
	err = conn.Export(introData, dbus.ObjectPath(dbusObjectPath), "org.freedesktop.DBus.Introspectable")
	if err != nil {
		conn.Close()
		return nil, err
	}

	// Store global reference
	dbusServiceMu.Lock()
	dbusService = service
	dbusServiceMu.Unlock()

	log.Printf("DBus service registered: %s", dbusServiceName)
	log.Printf("  Recheck (notify only): dbus-send --session --type=method_call --dest=%s %s %s.Recheck boolean:false",
		dbusServiceName, dbusObjectPath, dbusInterface)
	log.Printf("  Recheck (with open):   dbus-send --session --type=method_call --dest=%s %s %s.Recheck boolean:true",
		dbusServiceName, dbusObjectPath, dbusInterface)

	return service, nil
}

// SetRecheckHandler sets the callback for recheck requests
// The handler receives a boolean indicating whether to open browsers for live channels
func (s *DBusService) SetRecheckHandler(handler func(openBrowser bool)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recheckHandler = handler
}

// SetRestartHandler sets the callback for restart requests.
func (s *DBusService) SetRestartHandler(handler func(openBrowser bool)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.restartHandler = handler
}

// SetStatusHandler sets the callback for status requests.
// The handler should return the number of currently live streams and their names.
func (s *DBusService) SetStatusHandler(handler func() (int, []string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statusHandler = handler
}

// SetDetailedStatusHandler sets the callback for structured status requests.
func (s *DBusService) SetDetailedStatusHandler(handler func() (int, string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.detailedStatusHandler = handler
}

// Recheck is the DBus method that triggers a live channel check
// open: if true, opens browser for live channels based on config settings
// Called via: dbus-send --session --type=method_call --dest=com.github.TwitchNotifications /com/github/TwitchNotifications com.github.TwitchNotifications.Recheck boolean:false
func (s *DBusService) Recheck(open bool) *dbus.Error {
	s.mu.RLock()
	handler := s.recheckHandler
	s.mu.RUnlock()

	if handler != nil {
		log.Printf("Recheck triggered via DBus (open=%v)", open)
		go handler(open) // Run in goroutine to not block DBus
	} else {
		log.Println("DBus Recheck called but no handler registered")
	}

	return nil
}

// Restart is the DBus method that triggers a graceful application restart.
func (s *DBusService) Restart() *dbus.Error {
	s.mu.RLock()
	handler := s.restartHandler
	s.mu.RUnlock()

	if handler != nil {
		log.Println("Restart triggered via DBus")
		go handler(false) // Run in goroutine to not block DBus
	} else {
		log.Println("DBus Restart called but no handler registered")
	}

	return nil
}

// RestartOpen triggers a graceful restart whose initial live check opens configured channels.
func (s *DBusService) RestartOpen() *dbus.Error {
	s.mu.RLock()
	handler := s.restartHandler
	s.mu.RUnlock()

	if handler != nil {
		log.Println("Restart with auto-open triggered via DBus")
		go handler(true) // Run in goroutine to not block DBus
	} else {
		log.Println("DBus RestartOpen called but no handler registered")
	}

	return nil
}

// GetStatus returns whether the service is active and current live stream count.
func (s *DBusService) GetStatus() (bool, int32, []string, *dbus.Error) {
	s.mu.RLock()
	handler := s.statusHandler
	s.mu.RUnlock()

	liveCount := 0
	liveChannels := []string{}
	if handler != nil {
		liveCount, liveChannels = handler()
	}

	return true, int32(liveCount), liveChannels, nil
}

// GetDetailedStatus returns structured metadata for currently live channels.
func (s *DBusService) GetDetailedStatus() (bool, int32, string, *dbus.Error) {
	s.mu.RLock()
	handler := s.detailedStatusHandler
	s.mu.RUnlock()

	liveCount := 0
	liveChannelsJSON := "[]"
	if handler != nil {
		liveCount, liveChannelsJSON = handler()
	}

	return true, int32(liveCount), liveChannelsJSON, nil
}

// Ping is a simple health check method
// Returns "pong" if the service is running
func (s *DBusService) Ping() (string, *dbus.Error) {
	return "pong", nil
}

// Close closes the DBus connection
func (s *DBusService) Close() error {
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}

// GetDBusService returns the global DBus service instance
func GetDBusService() *DBusService {
	dbusServiceMu.RLock()
	defer dbusServiceMu.RUnlock()
	return dbusService
}

// CloseDBusService closes the global DBus service if it exists
func CloseDBusService() {
	dbusServiceMu.Lock()
	defer dbusServiceMu.Unlock()
	if dbusService != nil {
		if err := dbusService.Close(); err != nil {
			log.Printf("Error closing DBus service: %v", err)
		}
		dbusService = nil
	}
}
