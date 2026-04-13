package main

import (
	_ "embed"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
)

//go:embed introspect/collection.xml
var collectionIntrospectXML string

//go:embed introspect/item.xml
var itemIntrospectXML string

// Service implements org.freedesktop.Secret.Service
type Service struct {
	conn     *dbus.Conn
	store    *Store
	sessions *SessionManager

	mu      sync.RWMutex
	aliases map[string]string // alias name -> collection ID
}

func NewService(conn *dbus.Conn, store *Store, sessions *SessionManager) *Service {
	s := &Service{
		conn:     conn,
		store:    store,
		sessions: sessions,
		aliases:  map[string]string{"default": "default"},
	}
	s.ensureDefaultCollection()
	return s
}

func (s *Service) ensureDefaultCollection() {
	collections, _ := s.store.ListCollections()
	if slices.Contains(collections, "default") {
		return
	}
	if err := s.store.CreateCollection("default", "Default keyring"); err != nil {
		log.Printf("Failed to create default collection: %v", err)
	}
}

// OpenSession negotiates a session with the client
func (s *Service) OpenSession(algorithm string, input dbus.Variant) (dbus.Variant, dbus.ObjectPath, *dbus.Error) {
	switch algorithm {
	case algPlain:
		path := s.sessions.OpenPlain()
		s.exportSession(path)
		return dbus.MakeVariant(""), path, nil

	case algDH:
		clientPubKey, ok := input.Value().([]byte)
		if !ok {
			return dbus.Variant{}, "", dbus.NewError("org.freedesktop.DBus.Error.InvalidArgs", []any{"expected byte array for DH public key"})
		}
		serverPubKey, path, err := s.sessions.OpenDH(clientPubKey)
		if err != nil {
			return dbus.Variant{}, "", dbusErr("Failed", err.Error())
		}
		s.exportSession(path)
		return dbus.MakeVariant(serverPubKey), path, nil

	default:
		return dbus.Variant{}, "", dbus.NewError("org.freedesktop.DBus.Error.NotSupported", []any{fmt.Sprintf("unsupported algorithm: %s", algorithm)})
	}
}

func (s *Service) exportSession(path dbus.ObjectPath) {
	if err := s.conn.Export(&Session{manager: s.sessions, path: path}, path, sessionIface); err != nil {
		log.Printf("Failed to export session %s: %v", path, err)
	}
}

// CreateCollection creates a new collection
func (s *Service) CreateCollection(properties map[string]dbus.Variant, alias string) (dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	label := "Untitled Collection"
	if v, ok := properties["org.freedesktop.Secret.Collection.Label"]; ok {
		if l, ok := v.Value().(string); ok {
			label = l
		}
	}

	id := slugify(label)

	if alias != "" {
		s.mu.RLock()
		existingID, ok := s.aliases[alias]
		s.mu.RUnlock()
		if ok {
			return collectionPath(existingID), noPrompt(), nil
		}
	}

	if err := s.store.CreateCollection(id, label); err != nil {
		return dbus.ObjectPath("/"), noPrompt(), dbusErr("Failed", err.Error())
	}

	if alias != "" {
		s.mu.Lock()
		s.aliases[alias] = id
		s.mu.Unlock()
		s.exportCollectionAt(dbus.ObjectPath(fmt.Sprintf("%s/aliases/%s", servicePath, alias)), id)
	}

	path := collectionPath(id)
	s.exportCollectionAt(path, id)
	_ = s.conn.Emit(dbus.ObjectPath(servicePath), serviceIface+".CollectionCreated", path)

	return path, noPrompt(), nil
}

// SearchItems searches all collections for items matching attributes
func (s *Service) SearchItems(attributes map[string]string) ([]dbus.ObjectPath, []dbus.ObjectPath, *dbus.Error) {
	collections, err := s.store.ListCollections()
	if err != nil {
		return nil, nil, dbusErr("Failed", err.Error())
	}

	var unlocked []dbus.ObjectPath
	for _, collID := range collections {
		items, err := s.store.SearchItems(collID, attributes)
		if err != nil {
			continue
		}
		for _, ref := range items {
			unlocked = append(unlocked, itemPath(ref.CollectionID, ref.LabelSlug, ref.ItemID))
		}
	}
	return unlocked, nil, nil
}

// GetSecrets retrieves multiple secrets at once
func (s *Service) GetSecrets(items []dbus.ObjectPath, session dbus.ObjectPath) (map[dbus.ObjectPath]Secret, *dbus.Error) {
	transfer, ok := s.sessions.Get(session)
	if !ok {
		return nil, dbusErr("NoSession", "session not found")
	}

	result := make(map[dbus.ObjectPath]Secret)
	for _, path := range items {
		collID, labelSlug, itemID, err := parseItemPath(path)
		if err != nil {
			continue
		}
		value, err := s.store.GetSecret(collID, labelSlug, itemID)
		if err != nil {
			continue
		}
		secret, err := transfer.Encrypt(value, session)
		if err != nil {
			continue
		}
		result[path] = secret
	}
	return result, nil
}

// Lock is a no-op — age handles encryption at rest
func (s *Service) Lock(objects []dbus.ObjectPath) ([]dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	return nil, noPrompt(), nil
}

// Unlock is a no-op — everything is always unlocked
func (s *Service) Unlock(objects []dbus.ObjectPath) ([]dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	return objects, noPrompt(), nil
}

// ReadAlias resolves a collection alias
func (s *Service) ReadAlias(name string) (dbus.ObjectPath, *dbus.Error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if id, ok := s.aliases[name]; ok {
		return collectionPath(id), nil
	}
	return dbus.ObjectPath("/"), nil
}

// SetAlias sets a collection alias
func (s *Service) SetAlias(name string, collection dbus.ObjectPath) *dbus.Error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if collection == "/" || collection == "" {
		delete(s.aliases, name)
	} else {
		s.aliases[name] = strings.TrimPrefix(string(collection), servicePath+"/collection/")
	}
	return nil
}

// exportCollectionAt exports a collection at the given D-Bus path
func (s *Service) exportCollectionAt(path dbus.ObjectPath, id string) {
	coll := &Collection{conn: s.conn, store: s.store, sessions: s.sessions, id: id, service: s}
	if err := s.conn.Export(coll, path, collectionIface); err != nil {
		log.Printf("Failed to export collection %s: %v", path, err)
	}
	if err := s.conn.Export(&CollectionProperties{store: s.store, id: id}, path, "org.freedesktop.DBus.Properties"); err != nil {
		log.Printf("Failed to export collection properties %s: %v", path, err)
	}
	if err := s.conn.Export(introspect.Introspectable(collectionIntrospectXML), path, "org.freedesktop.DBus.Introspectable"); err != nil {
		log.Printf("Failed to export collection introspection %s: %v", path, err)
	}
}

func (s *Service) exportItem(ref ItemRef) {
	item := &Item{conn: s.conn, store: s.store, sessions: s.sessions, ref: ref}
	path := itemPath(ref.CollectionID, ref.LabelSlug, ref.ItemID)
	if err := s.conn.Export(item, path, itemIface); err != nil {
		log.Printf("Failed to export item %s: %v", path, err)
	}
	if err := s.conn.Export(&ItemProperties{store: s.store, ref: ref}, path, "org.freedesktop.DBus.Properties"); err != nil {
		log.Printf("Failed to export item properties %s: %v", path, err)
	}
	if err := s.conn.Export(introspect.Introspectable(itemIntrospectXML), path, "org.freedesktop.DBus.Introspectable"); err != nil {
		log.Printf("Failed to export item introspection %s: %v", path, err)
	}
}

// ExportAll exports all existing collections, items, and aliases on startup
func (s *Service) ExportAll() {
	collections, _ := s.store.ListCollections()
	for _, collID := range collections {
		s.exportCollectionAt(collectionPath(collID), collID)
		items, _ := s.store.ListItems(collID)
		for _, ref := range items {
			s.exportItem(ref)
		}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for alias, collID := range s.aliases {
		s.exportCollectionAt(dbus.ObjectPath(fmt.Sprintf("%s/aliases/%s", servicePath, alias)), collID)
	}
}

// -- Path helpers --

func collectionPath(id string) dbus.ObjectPath {
	return dbus.ObjectPath(fmt.Sprintf("%s/collection/%s", servicePath, id))
}

func itemPath(collectionID, labelSlug, itemID string) dbus.ObjectPath {
	return dbus.ObjectPath(fmt.Sprintf("%s/collection/%s/%s/%s", servicePath, collectionID, labelSlug, itemID))
}

func parseItemPath(path dbus.ObjectPath) (collectionID, labelSlug, itemID string, err error) {
	s := strings.TrimPrefix(string(path), servicePath+"/collection/")
	parts := strings.SplitN(s, "/", 3)
	if len(parts) != 3 {
		return "", "", "", fmt.Errorf("invalid item path: %s", path)
	}
	return parts[0], parts[1], parts[2], nil
}
