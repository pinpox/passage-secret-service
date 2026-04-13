package main

import (
	"github.com/godbus/dbus/v5"
)

// ServiceProperties implements org.freedesktop.DBus.Properties for the Service
type ServiceProperties struct {
	store *Store
}

func (p *ServiceProperties) Get(iface, property string) (dbus.Variant, *dbus.Error) {
	if iface != serviceIface {
		return dbus.Variant{}, dbus.NewError("org.freedesktop.DBus.Error.UnknownInterface", nil)
	}
	if property != "Collections" {
		return dbus.Variant{}, dbus.NewError("org.freedesktop.DBus.Error.UnknownProperty", nil)
	}
	collections, _ := p.store.ListCollections()
	paths := make([]dbus.ObjectPath, len(collections))
	for i, c := range collections {
		paths[i] = collectionPath(c)
	}
	return dbus.MakeVariant(paths), nil
}

func (p *ServiceProperties) GetAll(iface string) (map[string]dbus.Variant, *dbus.Error) {
	v, err := p.Get(iface, "Collections")
	if err != nil {
		return nil, err
	}
	return map[string]dbus.Variant{"Collections": v}, nil
}

func (p *ServiceProperties) Set(string, string, dbus.Variant) *dbus.Error {
	return dbus.NewError("org.freedesktop.DBus.Error.PropertyReadOnly", nil)
}

// CollectionProperties implements org.freedesktop.DBus.Properties for a Collection
type CollectionProperties struct {
	store *Store
	id    string
}

func (p *CollectionProperties) Get(iface, property string) (dbus.Variant, *dbus.Error) {
	if iface != collectionIface {
		return dbus.Variant{}, dbus.NewError("org.freedesktop.DBus.Error.UnknownInterface", nil)
	}
	switch property {
	case "Items":
		items, _ := p.store.ListItems(p.id)
		paths := make([]dbus.ObjectPath, len(items))
		for i, ref := range items {
			paths[i] = itemPath(ref.CollectionID, ref.LabelSlug, ref.ItemID)
		}
		return dbus.MakeVariant(paths), nil
	case "Label":
		meta, _ := p.store.GetCollectionMeta(p.id)
		if meta == nil {
			return dbus.MakeVariant(""), nil
		}
		return dbus.MakeVariant(meta.Label), nil
	case "Locked":
		return dbus.MakeVariant(false), nil
	case "Created", "Modified":
		meta, _ := p.store.GetCollectionMeta(p.id)
		if meta == nil {
			return dbus.MakeVariant(uint64(0)), nil
		}
		if property == "Created" {
			return dbus.MakeVariant(uint64(meta.Created)), nil
		}
		return dbus.MakeVariant(uint64(meta.Modified)), nil
	default:
		return dbus.Variant{}, dbus.NewError("org.freedesktop.DBus.Error.UnknownProperty", nil)
	}
}

func (p *CollectionProperties) GetAll(iface string) (map[string]dbus.Variant, *dbus.Error) {
	if iface != collectionIface {
		return nil, dbus.NewError("org.freedesktop.DBus.Error.UnknownInterface", nil)
	}
	items, _ := p.store.ListItems(p.id)
	paths := make([]dbus.ObjectPath, len(items))
	for i, ref := range items {
		paths[i] = itemPath(ref.CollectionID, ref.LabelSlug, ref.ItemID)
	}
	meta, _ := p.store.GetCollectionMeta(p.id)
	label := ""
	var created, modified uint64
	if meta != nil {
		label = meta.Label
		created = uint64(meta.Created)
		modified = uint64(meta.Modified)
	}
	return map[string]dbus.Variant{
		"Items":    dbus.MakeVariant(paths),
		"Label":    dbus.MakeVariant(label),
		"Locked":   dbus.MakeVariant(false),
		"Created":  dbus.MakeVariant(created),
		"Modified": dbus.MakeVariant(modified),
	}, nil
}

func (p *CollectionProperties) Set(iface, property string, value dbus.Variant) *dbus.Error {
	if iface == collectionIface && property == "Label" {
		if label, ok := value.Value().(string); ok {
			if err := p.store.SetCollectionLabel(p.id, label); err != nil {
				return dbusErr("Failed", err.Error())
			}
			return nil
		}
	}
	return dbus.NewError("org.freedesktop.DBus.Error.PropertyReadOnly", nil)
}

// ItemProperties implements org.freedesktop.DBus.Properties for an Item
type ItemProperties struct {
	store *Store
	ref   ItemRef
}

func (p *ItemProperties) Get(iface, property string) (dbus.Variant, *dbus.Error) {
	if iface != itemIface {
		return dbus.Variant{}, dbus.NewError("org.freedesktop.DBus.Error.UnknownInterface", nil)
	}
	if property == "Locked" {
		return dbus.MakeVariant(false), nil
	}
	meta, err := p.store.GetItemMeta(p.ref.CollectionID, p.ref.LabelSlug, p.ref.ItemID)
	if err != nil {
		return dbus.Variant{}, dbusErr("Failed", err.Error())
	}
	switch property {
	case "Attributes":
		attrs := meta.Attributes
		if attrs == nil {
			attrs = make(map[string]string)
		}
		return dbus.MakeVariant(attrs), nil
	case "Label":
		return dbus.MakeVariant(meta.Label), nil
	case "Created":
		return dbus.MakeVariant(uint64(meta.Created)), nil
	case "Modified":
		return dbus.MakeVariant(uint64(meta.Modified)), nil
	default:
		return dbus.Variant{}, dbus.NewError("org.freedesktop.DBus.Error.UnknownProperty", nil)
	}
}

func (p *ItemProperties) GetAll(iface string) (map[string]dbus.Variant, *dbus.Error) {
	if iface != itemIface {
		return nil, dbus.NewError("org.freedesktop.DBus.Error.UnknownInterface", nil)
	}
	meta, err := p.store.GetItemMeta(p.ref.CollectionID, p.ref.LabelSlug, p.ref.ItemID)
	if err != nil {
		return nil, dbusErr("Failed", err.Error())
	}
	attrs := meta.Attributes
	if attrs == nil {
		attrs = make(map[string]string)
	}
	return map[string]dbus.Variant{
		"Locked":     dbus.MakeVariant(false),
		"Attributes": dbus.MakeVariant(attrs),
		"Label":      dbus.MakeVariant(meta.Label),
		"Created":    dbus.MakeVariant(uint64(meta.Created)),
		"Modified":   dbus.MakeVariant(uint64(meta.Modified)),
	}, nil
}

func (p *ItemProperties) Set(iface, property string, value dbus.Variant) *dbus.Error {
	if iface != itemIface {
		return dbus.NewError("org.freedesktop.DBus.Error.UnknownInterface", nil)
	}
	meta, err := p.store.GetItemMeta(p.ref.CollectionID, p.ref.LabelSlug, p.ref.ItemID)
	if err != nil {
		return dbusErr("Failed", err.Error())
	}
	switch property {
	case "Label":
		if label, ok := value.Value().(string); ok {
			meta.Label = label
			if err := p.store.SetItemMeta(p.ref.CollectionID, p.ref.LabelSlug, p.ref.ItemID, meta); err != nil {
				return dbusErr("Failed", err.Error())
			}
			return nil
		}
	case "Attributes":
		if attrs, ok := value.Value().(map[string]string); ok {
			meta.Attributes = attrs
			if err := p.store.SetItemMeta(p.ref.CollectionID, p.ref.LabelSlug, p.ref.ItemID, meta); err != nil {
				return dbusErr("Failed", err.Error())
			}
			return nil
		}
	}
	return dbus.NewError("org.freedesktop.DBus.Error.PropertyReadOnly", nil)
}
