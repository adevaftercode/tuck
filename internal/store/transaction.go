package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"runtime"
	"strings"
)

const transactionDirectory = ".weft-txn"

type Change struct {
	Path   string
	Data   []byte
	Delete bool
}

type transactionManifest struct {
	Version int                  `json:"version"`
	Changes []transactionChange `json:"changes"`
}

type transactionChange struct {
	Path         string `json:"path"`
	BeforeExists bool   `json:"before_exists"`
	BeforeHash   string `json:"before_hash,omitempty"`
	BeforeMode   uint32 `json:"before_mode,omitempty"`
	AfterExists  bool   `json:"after_exists"`
	AfterHash    string `json:"after_hash,omitempty"`
	BeforeFile   string `json:"before_file,omitempty"`
	AfterFile    string `json:"after_file,omitempty"`
}

func Recover(root string) error {
	journal := filepath.Join(root, transactionDirectory)
	info, err := os.Lstat(journal)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("transaction recovery conflict: %s is not a directory", journal)
	}
	manifestPath := filepath.Join(journal, "manifest.json")
	manifestInfo, err := os.Lstat(manifestPath)
	if errors.Is(err, fs.ErrNotExist) {
		// A manifest is installed only after all before/after images are durable;
		// no board file is changed before that point.
		return cleanupJournal(root, journal)
	}
	if err != nil {
		return err
	}
	if !manifestInfo.Mode().IsRegular() {
		return fmt.Errorf("transaction recovery conflict: manifest is not a regular file")
	}
	encoded, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	if markerInfo, err := os.Lstat(filepath.Join(journal, "COMMITTED")); err == nil {
		if !markerInfo.Mode().IsRegular() {
			return fmt.Errorf("transaction recovery conflict: committed marker is not a regular file")
		}
		return cleanupJournal(root, journal)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	var manifest transactionManifest
	if err := json.Unmarshal(encoded, &manifest); err != nil || manifest.Version != 1 {
		return fmt.Errorf("transaction recovery conflict: invalid manifest in %s", journal)
	}
	if err := validateManifest(manifest); err != nil {
		return fmt.Errorf("transaction recovery conflict: %w", err)
	}
	imagesInfo, err := os.Lstat(filepath.Join(journal, "images"))
	if err != nil || !imagesInfo.IsDir() || imagesInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("transaction recovery conflict: staged images directory is missing or invalid")
	}
	for _, change := range manifest.Changes {
		if err := applyJournalChange(root, journal, change); err != nil {
			return fmt.Errorf("transaction recovery conflict for %s: %w", change.Path, err)
		}
	}
	cleanedDirectories := make(map[string]bool)
	for _, change := range manifest.Changes {
		target := filepath.Join(root, filepath.FromSlash(change.Path))
		directory := filepath.Dir(target)
		if cleanedDirectories[directory] {
			continue
		}
		if err := removeWriteTemps(directory); err != nil {
			return fmt.Errorf("clean interrupted writes in %s: %w", directory, err)
		}
		cleanedDirectories[directory] = true
	}
	marker := filepath.Join(journal, "COMMITTED")
	if err := writeSynced(marker, []byte("committed\n"), 0o600); err != nil {
		return err
	}
	if err := syncDirectory(journal); err != nil {
		return err
	}
	return cleanupJournal(root, journal)
}

// HasPendingRecovery lets check report an interrupted transaction without
// changing the board. All other commands recover while holding the board lock.
func HasPendingRecovery(root string) (bool, error) {
	info, err := os.Lstat(filepath.Join(root, transactionDirectory))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return true, nil
	}
	return true, nil
}

func Commit(root string, changes []Change) error {
	if len(changes) == 0 {
		return nil
	}
	if err := Recover(root); err != nil {
		return err
	}
	seen := make(map[string]bool, len(changes))
	for _, change := range changes {
		rel, err := safeRelative(change.Path)
		if err != nil {
			return err
		}
		change.Path = rel
		if seen[rel] {
			return fmt.Errorf("transaction contains duplicate path %q", rel)
		}
		seen[rel] = true
		if err := checkSafeParents(root, rel); err != nil {
			return err
		}
	}

	journal := filepath.Join(root, transactionDirectory)
	if err := os.Mkdir(journal, 0o700); err != nil {
		return fmt.Errorf("create transaction journal: %w", err)
	}
	staged := filepath.Join(journal, "images")
	if err := os.Mkdir(staged, 0o700); err != nil {
		_ = os.RemoveAll(journal)
		return err
	}
	manifest := transactionManifest{Version: 1, Changes: make([]transactionChange, 0, len(changes))}
	for i, change := range changes {
		rel, _ := safeRelative(change.Path)
		target := filepath.Join(root, filepath.FromSlash(rel))
		entry := transactionChange{Path: rel, AfterExists: !change.Delete}
		if info, err := os.Lstat(target); err == nil {
			if !info.Mode().IsRegular() {
				_ = os.RemoveAll(journal)
				return fmt.Errorf("refusing to replace non-regular file %s", rel)
			}
			before, err := os.ReadFile(target)
			if err != nil {
				_ = os.RemoveAll(journal)
				return err
			}
			entry.BeforeExists = true
			entry.BeforeHash = hash(before)
			entry.BeforeMode = uint32(info.Mode().Perm())
			entry.BeforeFile = fmt.Sprintf("before-%06d", i)
			if err := writeSynced(filepath.Join(staged, entry.BeforeFile), before, 0o600); err != nil {
				_ = os.RemoveAll(journal)
				return err
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			_ = os.RemoveAll(journal)
			return err
		}
		if !change.Delete {
			entry.AfterHash = hash(change.Data)
			entry.AfterFile = fmt.Sprintf("after-%06d", i)
			if err := writeSynced(filepath.Join(staged, entry.AfterFile), change.Data, 0o600); err != nil {
				_ = os.RemoveAll(journal)
				return err
			}
		}
		manifest.Changes = append(manifest.Changes, entry)
	}
	if err := syncDirectory(staged); err != nil {
		_ = os.RemoveAll(journal)
		return err
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		_ = os.RemoveAll(journal)
		return err
	}
	manifestTemp := filepath.Join(journal, "manifest.tmp")
	if err := writeSynced(manifestTemp, append(encoded, '\n'), 0o600); err != nil {
		_ = os.RemoveAll(journal)
		return err
	}
	if err := replaceFile(manifestTemp, filepath.Join(journal, "manifest.json")); err != nil {
		_ = os.RemoveAll(journal)
		return err
	}
	if err := syncDirectory(journal); err != nil {
		_ = os.RemoveAll(journal)
		return err
	}
	if err := syncDirectory(root); err != nil {
		_ = os.RemoveAll(journal)
		return err
	}
	return Recover(root)
}

func applyJournalChange(root, journal string, change transactionChange) error {
	rel, err := safeRelative(change.Path)
	if err != nil {
		return err
	}
	if err := checkSafeParents(root, rel); err != nil {
		return err
	}
	target := filepath.Join(root, filepath.FromSlash(rel))
	current, currentExists, mode, err := readRegular(target)
	if err != nil {
		return err
	}
	if matches(current, currentExists, change.AfterHash, change.AfterExists) {
		return nil
	}
	if !matches(current, currentExists, change.BeforeHash, change.BeforeExists) {
		return errors.New("current file matches neither the recorded old nor new content; leaving it untouched")
	}
	if !change.AfterExists {
		if currentExists {
			if err := os.Remove(target); err != nil {
				return err
			}
			return syncDirectory(filepath.Dir(target))
		}
		return nil
	}
	content, exists, _, err := readRegular(filepath.Join(journal, "images", change.AfterFile))
	if err != nil {
		return fmt.Errorf("read staged content: %w", err)
	}
	if !exists {
		return errors.New("staged content is missing")
	}
	if hash(content) != change.AfterHash {
		return errors.New("staged content hash does not match transaction manifest")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if mode == 0 {
		mode = fs.FileMode(change.BeforeMode)
	}
	if mode == 0 {
		mode = 0o644
	}
	return writeAtomic(target, content, mode)
}

func readRegular(path string) ([]byte, bool, fs.FileMode, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, 0, nil
	}
	if err != nil {
		return nil, false, 0, err
	}
	if !info.Mode().IsRegular() {
		return nil, false, 0, fmt.Errorf("refusing to operate on non-regular file %s", path)
	}
	data, err := os.ReadFile(path)
	return data, true, info.Mode().Perm(), err
}

func matches(data []byte, exists bool, expected string, expectedExists bool) bool {
	if exists != expectedExists {
		return false
	}
	if !exists {
		return true
	}
	return hash(data) == expected
}

func writeAtomic(destination string, data []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(destination), ".weft-write-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(mode.Perm()); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := replaceFile(tempPath, destination); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(destination))
}

func writeSynced(path string, data []byte, mode fs.FileMode) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func cleanupJournal(root, journal string) error {
	if err := os.RemoveAll(journal); err != nil {
		return err
	}
	return syncDirectory(root)
}

func removeWriteTemps(directory string) error {
	entries, err := os.ReadDir(directory)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	removed := false
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".weft-write-") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to remove non-regular temporary file %s", path)
		}
		if err := os.Remove(path); err != nil {
			return err
		}
		removed = true
	}
	if removed {
		return syncDirectory(directory)
	}
	return nil
}

func syncDirectory(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func safeRelative(path string) (string, error) {
	normalized := strings.ReplaceAll(path, `\`, "/")
	if path == "" || filepath.IsAbs(path) || strings.HasPrefix(normalized, "/") ||
		len(normalized) >= 2 && ((normalized[0] >= 'A' && normalized[0] <= 'Z') || (normalized[0] >= 'a' && normalized[0] <= 'z')) && normalized[1] == ':' {
		return "", fmt.Errorf("transaction path %q must be relative", path)
	}
	clean := pathpkg.Clean(normalized)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean != normalized {
		return "", fmt.Errorf("transaction path %q is not a canonical board-relative path", path)
	}
	if clean == "WEFT.md" {
		return clean, nil
	}
	parts := strings.Split(clean, "/")
	if len(parts) != 3 || parts[0] != "tasks" || !validTaskState(parts[1]) || parts[2] == "." || filepath.Ext(parts[2]) != ".md" {
		return "", fmt.Errorf("transaction path %q must name WEFT.md or a task Markdown file", path)
	}
	return clean, nil
}

func validTaskState(state string) bool {
	switch state {
	case "backlog", "todo", "doing", "done":
		return true
	default:
		return false
	}
}

func checkSafeParents(root, rel string) error {
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("board root %s must be a real directory", root)
	}
	parts := strings.Split(rel, "/")
	if len(parts) < 2 || parts[0] != "tasks" {
		return nil
	}
	current := root
	for _, component := range parts[:len(parts)-1] {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to traverse non-directory or symlink path %s", current)
		}
	}
	return nil
}

func validateManifest(manifest transactionManifest) error {
	seen := make(map[string]bool, len(manifest.Changes))
	for _, change := range manifest.Changes {
		rel, err := safeRelative(change.Path)
		if err != nil || rel != change.Path {
			return fmt.Errorf("invalid transaction path %q", change.Path)
		}
		if seen[rel] {
			return fmt.Errorf("duplicate transaction path %q", rel)
		}
		seen[rel] = true
		if change.BeforeExists {
			if !validHash(change.BeforeHash) || !validImageName(change.BeforeFile, "before-") {
				return fmt.Errorf("invalid before image for %q", rel)
			}
		} else if change.BeforeHash != "" || change.BeforeFile != "" {
			return fmt.Errorf("unexpected before image for %q", rel)
		}
		if change.AfterExists {
			if !validHash(change.AfterHash) || !validImageName(change.AfterFile, "after-") {
				return fmt.Errorf("invalid after image for %q", rel)
			}
		} else if change.AfterHash != "" || change.AfterFile != "" {
			return fmt.Errorf("unexpected after image for %q", rel)
		}
	}
	return nil
}

func validHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validImageName(value, prefix string) bool {
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+6 {
		return false
	}
	for _, digit := range value[len(prefix):] {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}

func hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
