package main

import (
	"github.com/godbus/dbus/v5"
)

const (
	busName     = "org.freedesktop.secrets"
	servicePath = "/org/freedesktop/secrets"

	serviceIface    = "org.freedesktop.Secret.Service"
	collectionIface = "org.freedesktop.Secret.Collection"
	itemIface       = "org.freedesktop.Secret.Item"
	sessionIface    = "org.freedesktop.Secret.Session"
	// promptIface     = "org.freedesktop.Secret.Prompt"

	algPlain = "plain"
	algDH    = "dh-ietf1024-sha256-aes128-cbc-pkcs7"
)

// Secret is the D-Bus secret structure: (session, parameters, value, content_type)
type Secret struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

// noPrompt returns the special "/" path meaning no prompt needed.
// The Secret Service spec allows operations to return a prompt object
// for user interaction (e.g. "enter master password"). We never need
// prompts since age identity files handle auth, so we always return "/".
func noPrompt() dbus.ObjectPath {
	return dbus.ObjectPath("/")
}

func dbusErr(name, msg string) *dbus.Error {
	return dbus.NewError("org.freedesktop.Secret.Error."+name, []any{msg})
}
