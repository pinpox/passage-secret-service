package main

import (
	"log"

	"github.com/godbus/dbus/v5"
)

// Collection implements org.freedesktop.Secret.Collection
type Collection struct {
	conn     *dbus.Conn
	store    *Store
	sessions *SessionManager
	id       string
	service  *Service
}

// Delete removes this collection
func (c *Collection) Delete() (dbus.ObjectPath, *dbus.Error) {
	if err := c.store.DeleteCollection(c.id); err != nil {
		return dbus.ObjectPath("/"), dbusErr("Failed", err.Error())
	}
	_ = c.conn.Emit(dbus.ObjectPath(servicePath), serviceIface+".CollectionDeleted", collectionPath(c.id))
	return noPrompt(), nil
}

// SearchItems searches for items matching the given attributes
func (c *Collection) SearchItems(attributes map[string]string) ([]dbus.ObjectPath, *dbus.Error) {
	items, err := c.store.SearchItems(c.id, attributes)
	if err != nil {
		return nil, dbusErr("Failed", err.Error())
	}
	var paths []dbus.ObjectPath
	for _, ref := range items {
		paths = append(paths, itemPath(ref.CollectionID, ref.LabelSlug, ref.ItemID))
	}
	return paths, nil
}

// CreateItem creates a new item in this collection
func (c *Collection) CreateItem(properties map[string]dbus.Variant, secret Secret, replace bool) (dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	label := ""
	if v, ok := properties["org.freedesktop.Secret.Item.Label"]; ok {
		if l, ok := v.Value().(string); ok {
			label = l
		}
	}

	attributes := make(map[string]string)
	if v, ok := properties["org.freedesktop.Secret.Item.Attributes"]; ok {
		switch attrs := v.Value().(type) {
		case map[string]string:
			attributes = attrs
		case map[string]dbus.Variant:
			for k, val := range attrs {
				if s, ok := val.Value().(string); ok {
					attributes[k] = s
				}
			}
		}
	}

	transfer, ok := c.sessions.Get(secret.Session)
	if !ok {
		return dbus.ObjectPath("/"), noPrompt(), dbusErr("NoSession", "session not found")
	}
	value, err := transfer.Decrypt(secret)
	if err != nil {
		return dbus.ObjectPath("/"), noPrompt(), dbusErr("Failed", "decrypt: "+err.Error())
	}

	// Replace existing item with matching attributes
	if replace && len(attributes) > 0 {
		items, _ := c.store.SearchItems(c.id, attributes)
		if len(items) > 0 {
			ref := items[0]
			if err := c.store.SetSecret(ref.CollectionID, ref.LabelSlug, ref.ItemID, value); err != nil {
				return dbus.ObjectPath("/"), noPrompt(), dbusErr("Failed", "set secret: "+err.Error())
			}
			if meta, err := c.store.GetItemMeta(ref.CollectionID, ref.LabelSlug, ref.ItemID); err == nil {
				if label != "" {
					meta.Label = label
				}
				meta.Attributes = attributes
				if err := c.store.SetItemMeta(ref.CollectionID, ref.LabelSlug, ref.ItemID, meta); err != nil {
					log.Printf("Failed to update item metadata: %v", err)
				}
			}
			path := itemPath(ref.CollectionID, ref.LabelSlug, ref.ItemID)
			_ = c.conn.Emit(collectionPath(c.id), collectionIface+".ItemChanged", path)
			return path, noPrompt(), nil
		}
	}

	labelSlug, itemID, err := c.store.CreateItem(c.id, label, attributes, value)
	if err != nil {
		log.Printf("CreateItem error: %v", err)
		return dbus.ObjectPath("/"), noPrompt(), dbusErr("Failed", "create: "+err.Error())
	}

	ref := ItemRef{CollectionID: c.id, LabelSlug: labelSlug, ItemID: itemID}
	c.service.exportItem(ref)

	path := itemPath(c.id, labelSlug, itemID)
	_ = c.conn.Emit(collectionPath(c.id), collectionIface+".ItemCreated", path)
	return path, noPrompt(), nil
}
