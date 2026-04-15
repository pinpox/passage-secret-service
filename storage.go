package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ItemMeta is the JSON sidecar for each item
type ItemMeta struct {
	Label      string            `json:"label"`
	Attributes map[string]string `json:"attributes"`
	Created    int64             `json:"created"`
	Modified   int64             `json:"modified"`
}

// CollectionMeta is the JSON metadata for a collection
type CollectionMeta struct {
	Label    string `json:"label"`
	Created  int64  `json:"created"`
	Modified int64  `json:"modified"`
}

// ItemRef identifies an item in the store
type ItemRef struct {
	CollectionID string
	LabelSlug    string
	ItemID       string
}

// Store handles passage CLI operations and metadata (JSON files)
type Store struct {
	metaDir string // ~/.local/share/passage-secret-service/
}

// Metadata layout:
// <metaDir>/
// ├── default/
// │   ├── .collection.json
// │   └── <labelSlug>/
// │       └── <itemID>.json
// └── other-collection/
//     └── ...

func NewStore(metaDir string) (*Store, error) {
	if err := os.MkdirAll(metaDir, 0700); err != nil {
		return nil, fmt.Errorf("create metadata dir: %w", err)
	}
	return &Store{metaDir: metaDir}, nil
}

func (s *Store) collectionDir(id string) string {
	return filepath.Join(s.metaDir, id)
}

func (s *Store) itemMetaPath(collectionID, labelSlug, itemID string) string {
	return filepath.Join(s.metaDir, collectionID, labelSlug, itemID+".json")
}

// passagePath returns a passage-compatible path (relative to store root)
func passagePath(collectionID, labelSlug, itemID string) string {
	return filepath.Join("secret-service", collectionID, labelSlug, itemID)
}

// -- Collection operations --

func (s *Store) ListCollections() ([]string, error) {
	entries, err := os.ReadDir(s.metaDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var collections []string
	for _, e := range entries {
		if e.IsDir() {
			collections = append(collections, e.Name())
		}
	}
	return collections, nil
}

func (s *Store) CreateCollection(id, label string) error {
	dir := s.collectionDir(id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	now := time.Now().Unix()
	return saveJSON(filepath.Join(dir, ".collection.json"), &CollectionMeta{
		Label:    label,
		Created:  now,
		Modified: now,
	})
}

func (s *Store) GetCollectionMeta(id string) (*CollectionMeta, error) {
	var meta CollectionMeta
	if err := loadJSON(filepath.Join(s.collectionDir(id), ".collection.json"), &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

func (s *Store) SetCollectionLabel(id, label string) error {
	meta, err := s.GetCollectionMeta(id)
	if err != nil {
		return err
	}
	meta.Label = label
	meta.Modified = time.Now().Unix()
	return saveJSON(filepath.Join(s.collectionDir(id), ".collection.json"), meta)
}

func (s *Store) DeleteCollection(id string) error {
	// Delete all passage entries for this collection
	items, _ := s.ListItems(id)
	for _, ref := range items {
		_ = passageRm(passagePath(ref.CollectionID, ref.LabelSlug, ref.ItemID))
	}
	return os.RemoveAll(s.collectionDir(id))
}

// -- Item operations --

func (s *Store) CreateItem(collectionID, label string, attributes map[string]string, secret []byte) (labelSlug, itemID string, err error) {
	labelSlug = slugify(label)
	itemID = uuid.New().String()[:8]

	if err := passageInsert(passagePath(collectionID, labelSlug, itemID), secret); err != nil {
		return "", "", fmt.Errorf("passage insert: %w", err)
	}

	dir := filepath.Join(s.metaDir, collectionID, labelSlug)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", "", fmt.Errorf("mkdir: %w", err)
	}

	now := time.Now().Unix()
	meta := ItemMeta{Label: label, Attributes: attributes, Created: now, Modified: now}
	if err := saveJSON(s.itemMetaPath(collectionID, labelSlug, itemID), &meta); err != nil {
		return "", "", fmt.Errorf("write meta: %w", err)
	}

	s.touchCollection(collectionID)
	return labelSlug, itemID, nil
}

func (s *Store) GetItemMeta(collectionID, labelSlug, itemID string) (*ItemMeta, error) {
	var meta ItemMeta
	if err := loadJSON(s.itemMetaPath(collectionID, labelSlug, itemID), &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

func (s *Store) SetItemMeta(collectionID, labelSlug, itemID string, meta *ItemMeta) error {
	meta.Modified = time.Now().Unix()
	return saveJSON(s.itemMetaPath(collectionID, labelSlug, itemID), meta)
}

func (s *Store) GetSecret(collectionID, labelSlug, itemID string) ([]byte, error) {
	return passageShow(passagePath(collectionID, labelSlug, itemID))
}

func (s *Store) SetSecret(collectionID, labelSlug, itemID string, secret []byte) error {
	return passageInsert(passagePath(collectionID, labelSlug, itemID), secret)
}

func (s *Store) DeleteItem(collectionID, labelSlug, itemID string) error {
	_ = passageRm(passagePath(collectionID, labelSlug, itemID))
	_ = os.Remove(s.itemMetaPath(collectionID, labelSlug, itemID))

	// Clean up empty label directory
	dir := filepath.Join(s.metaDir, collectionID, labelSlug)
	if entries, _ := os.ReadDir(dir); len(entries) == 0 {
		_ = os.Remove(dir)
	}

	s.touchCollection(collectionID)
	return nil
}

func (s *Store) ListItems(collectionID string) ([]ItemRef, error) {
	collDir := s.collectionDir(collectionID)
	labelDirs, err := os.ReadDir(collDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var items []ItemRef
	for _, ld := range labelDirs {
		if !ld.IsDir() || strings.HasPrefix(ld.Name(), ".") {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(collDir, ld.Name()))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".json") {
				items = append(items, ItemRef{
					CollectionID: collectionID,
					LabelSlug:    ld.Name(),
					ItemID:       strings.TrimSuffix(e.Name(), ".json"),
				})
			}
		}
	}
	return items, nil
}

func (s *Store) SearchItems(collectionID string, attributes map[string]string) ([]ItemRef, error) {
	all, err := s.ListItems(collectionID)
	if err != nil {
		return nil, err
	}

	var matched []ItemRef
	for _, ref := range all {
		meta, err := s.GetItemMeta(ref.CollectionID, ref.LabelSlug, ref.ItemID)
		if err != nil {
			continue
		}
		if matchAttributes(meta.Attributes, attributes) {
			matched = append(matched, ref)
		}
	}
	return matched, nil
}

func matchAttributes(stored, query map[string]string) bool {
	for k, v := range query {
		if stored[k] != v {
			return false
		}
	}
	return true
}

func (s *Store) touchCollection(id string) {
	meta, err := s.GetCollectionMeta(id)
	if err != nil {
		return
	}
	meta.Modified = time.Now().Unix()
	if err := saveJSON(filepath.Join(s.collectionDir(id), ".collection.json"), meta); err != nil {
		log.Printf("Failed to update collection %s modified time: %v", id, err)
	}
}

// -- passage CLI wrappers --

func passageInsert(path string, secret []byte) error {
	cmd := exec.Command("passage", "insert", "-m", "-f", path)
	cmd.Stdin = bytes.NewReader(secret)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func passageShow(path string) ([]byte, error) {
	cmd := exec.Command("passage", "show", path)
	cmd.Stderr = os.Stderr
	return cmd.Output()
}

func passageRm(path string) error {
	cmd := exec.Command("passage", "rm", "-f", path)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// -- helpers --

// slugify converts a label to a filesystem-safe directory name.
// Uses underscores since D-Bus object paths only allow [A-Za-z0-9_].
var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = slugRe.ReplaceAllString(strings.ToLower(s), "_")
	s = strings.Trim(s, "_")
	if s == "" {
		s = "unlabeled"
	}
	return s
}

func loadJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func saveJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
