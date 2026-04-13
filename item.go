package main

import (
	"github.com/godbus/dbus/v5"
)

// Item implements org.freedesktop.Secret.Item
type Item struct {
	conn     *dbus.Conn
	store    *Store
	sessions *SessionManager
	ref      ItemRef
}

// Delete removes this item
func (i *Item) Delete() (dbus.ObjectPath, *dbus.Error) {
	if err := i.store.DeleteItem(i.ref.CollectionID, i.ref.LabelSlug, i.ref.ItemID); err != nil {
		return dbus.ObjectPath("/"), dbusErr("Failed", err.Error())
	}
	_ = i.conn.Emit(collectionPath(i.ref.CollectionID), collectionIface+".ItemDeleted",
		itemPath(i.ref.CollectionID, i.ref.LabelSlug, i.ref.ItemID))
	return noPrompt(), nil
}

// GetSecret retrieves the secret value
func (i *Item) GetSecret(session dbus.ObjectPath) (Secret, *dbus.Error) {
	transfer, ok := i.sessions.Get(session)
	if !ok {
		return Secret{}, dbusErr("NoSession", "session not found")
	}
	value, err := i.store.GetSecret(i.ref.CollectionID, i.ref.LabelSlug, i.ref.ItemID)
	if err != nil {
		return Secret{}, dbusErr("Failed", "get secret: "+err.Error())
	}
	secret, err := transfer.Encrypt(value, session)
	if err != nil {
		return Secret{}, dbusErr("Failed", "encrypt: "+err.Error())
	}
	return secret, nil
}

// SetSecret updates the secret value
func (i *Item) SetSecret(secret Secret) *dbus.Error {
	transfer, ok := i.sessions.Get(secret.Session)
	if !ok {
		return dbusErr("NoSession", "session not found")
	}
	value, err := transfer.Decrypt(secret)
	if err != nil {
		return dbusErr("Failed", "decrypt: "+err.Error())
	}
	if err := i.store.SetSecret(i.ref.CollectionID, i.ref.LabelSlug, i.ref.ItemID, value); err != nil {
		return dbusErr("Failed", "set secret: "+err.Error())
	}
	_ = i.conn.Emit(collectionPath(i.ref.CollectionID), collectionIface+".ItemChanged",
		itemPath(i.ref.CollectionID, i.ref.LabelSlug, i.ref.ItemID))
	return nil
}
