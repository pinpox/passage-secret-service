package main

import (
	_ "embed"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
)

//go:embed introspect/service.xml
var serviceIntrospectXML string

func dataDir() string {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "passage-secret-service")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "passage-secret-service")
}

func main() {

	// Connect to dbus
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		log.Fatalf("Failed to connect to session bus: %v", err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			log.Printf("Failed to close D-Bus connection: %v", err)
		}
	}()

	reply, err := conn.RequestName(busName, dbus.NameFlagDoNotQueue|dbus.NameFlagReplaceExisting)
	if err != nil {
		log.Fatalf("Failed to request name %s: %v", busName, err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		log.Fatalf("Name %s already taken", busName)
	}

	store, err := NewStore(dataDir())
	if err != nil {
		log.Fatalf("Failed to initialize metadata store: %v", err)
	}

	sessions := NewSessionManager()
	service := NewService(conn, store, sessions)

	// Register our interfaces as D-Bus services
	// Actual service methods
	if err := conn.Export(service, dbus.ObjectPath(servicePath), serviceIface); err != nil {
		log.Fatalf("Failed to export service: %v", err)
	}

	// Access to service properties
	if err := conn.Export(&ServiceProperties{store: store}, dbus.ObjectPath(servicePath), "org.freedesktop.DBus.Properties"); err != nil {
		log.Fatalf("Failed to export service properties: %v", err)
	}

	//Allows clients to discover API. While the API is standarized some
	//applicatinos (e.g. Gitbhutler) call this during initialization to
	//validate it provides the methods it expects
	if err := conn.Export(introspect.Introspectable(serviceIntrospectXML), dbus.ObjectPath(servicePath), "org.freedesktop.DBus.Introspectable"); err != nil {
		log.Fatalf("Failed to export service introspection: %v", err)
	}

	service.ExportAll()

	fmt.Println("passage-secret-service: listening on D-Bus session bus")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	fmt.Println("\nShutting down")
}
