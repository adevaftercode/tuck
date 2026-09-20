package store

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"runtime"
	"strings"
)

const transactionDirectory = ".tuck-txn"

type Change struct {
	Path   string
	Data   []byte
	Delete bool
}

type transactionManifest struct {
	Version int                 `json:"version"`
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
	boardRoot, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer boardRoot.Close()

	journalInfo, err := boardRoot.Lstat(transactionDirectory)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !realDirectory(journalInfo) {
		return fmt.Errorf("transaction recovery conflict: %s is not a real directory", transactionDirectory)
	}
	journalRoot, err := openCheckedDirectory(boardRoot, transactionDirectory, journalInfo)
	if err != nil {
		return fmt.Errorf("transaction recovery conflict: %w", err)
	}
	cleanup, recoverErr := recoverJournal(boardRoot, journalRoot)
	closeErr := journalRoot.Close()
	if recoverErr != nil {
		return recoverErr
	}
	if closeErr != nil {
		return closeErr
	}
	if cleanup {
		return cleanupJournal(boardRoot)
	}
	return nil
}

func recoverJournal(boardRoot, journalRoot *os.Root) (bool, error) {
	encoded, manifestExists, _, err := readRegular(journalRoot, "manifest.json")
	if errors.Is(err, fs.ErrNotExist) || err == nil && !manifestExists {
		// No board file is changed until the manifest is installed.
		return true, nil
	}
	if err != nil {
		return false, err
	}
	_, markerExists, _, err := readRegular(journalRoot, "COMMITTED")
	if err == nil && markerExists {
		return true, nil
	} else if err != nil {
		return false, err
	}
	var manifest transactionManifest
	if err := json.Unmarshal(encoded, &manifest); err != nil || manifest.Version != 1 {
		return false, fmt.Errorf("transaction recovery conflict: invalid manifest in %s", transactionDirectory)
	}
	if err := validateManifest(manifest); err != nil {
		return false, fmt.Errorf("transaction recovery conflict: %w", err)
	}
	imagesInfo, err := journalRoot.Lstat("images")
	if err != nil || !realDirectory(imagesInfo) {
		return false, fmt.Errorf("transaction recovery conflict: staged images directory is missing or invalid")
	}
	imagesRoot, err := openCheckedDirectory(journalRoot, "images", imagesInfo)
	if err != nil {
		return false, fmt.Errorf("transaction recovery conflict: staged images directory is missing or invalid: %w", err)
	}
	defer imagesRoot.Close()

	for _, change := range manifest.Changes {
		if err := applyJournalChange(boardRoot, imagesRoot, change); err != nil {
			return false, fmt.Errorf("transaction recovery conflict for %s: %w", change.Path, err)
		}
	}
	cleanedDirectories := make(map[string]bool)
	for _, change := range manifest.Changes {
		parent, _, closeParent, err := openTarget(boardRoot, change.Path, false)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("clean interrupted writes in %s: %w", change.Path, err)
		}
		key := "."
		if slash := strings.LastIndex(change.Path, "/"); slash >= 0 {
			key = change.Path[:slash]
		}
		if cleanedDirectories[key] {
			closeParent()
			continue
		}
		if err := removeWriteTemps(parent); err != nil {
			closeParent()
			return false, fmt.Errorf("clean interrupted writes in %s: %w", key, err)
		}
		closeParent()
		cleanedDirectories[key] = true
	}
	if err := writeSynced(journalRoot, "COMMITTED", []byte("committed\n"), 0o600); err != nil {
		return false, err
	}
	if err := syncRootDirectory(journalRoot); err != nil {
		return false, err
	}
	return true, nil
}

// HasPendingRecovery lets check report an interrupted transaction without
// changing the board. All other commands recover while holding the board lock.
func HasPendingRecovery(root string) (bool, error) {
	boardRoot, err := os.OpenRoot(root)
	if err != nil {
		return false, err
	}
	defer boardRoot.Close()
	_, err = boardRoot.Lstat(transactionDirectory)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func Commit(root string, changes []Change) error {
	if len(changes) == 0 {
		return nil
	}
	changes = append([]Change(nil), changes...)
	if err := Recover(root); err != nil {
		return err
	}
	seen := make(map[string]bool, len(changes))
	for i := range changes {
		rel, err := safeRelative(changes[i].Path)
		if err != nil {
			return err
		}
		changes[i].Path = rel
		if seen[rel] {
			return fmt.Errorf("transaction contains duplicate path %q", rel)
		}
		seen[rel] = true
	}
	boardRoot, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer boardRoot.Close()
	if err := prepareAndApply(boardRoot, changes); err != nil {
		return err
	}
	return Recover(root)
}

func prepareAndApply(boardRoot *os.Root, changes []Change) (retErr error) {
	if err := boardRoot.Mkdir(transactionDirectory, 0o700); err != nil {
		return fmt.Errorf("create transaction journal: %w", err)
	}
	defer func() {
		if retErr != nil {
			if err := cleanupJournal(boardRoot); err != nil {
				retErr = errors.Join(retErr, err)
			}
		}
	}()

	journalInfo, err := boardRoot.Lstat(transactionDirectory)
	if err != nil || !realDirectory(journalInfo) {
		return fmt.Errorf("create transaction journal: journal path is not a real directory")
	}
	journalRoot, err := openCheckedDirectory(boardRoot, transactionDirectory, journalInfo)
	if err != nil {
		return fmt.Errorf("create transaction journal: %w", err)
	}
	defer journalRoot.Close()
	if err := journalRoot.Mkdir("images", 0o700); err != nil {
		return err
	}
	imagesInfo, err := journalRoot.Lstat("images")
	if err != nil || !realDirectory(imagesInfo) {
		return fmt.Errorf("staged images path is not a real directory")
	}
	imagesRoot, err := openCheckedDirectory(journalRoot, "images", imagesInfo)
	if err != nil {
		return err
	}
	defer imagesRoot.Close()

	manifest := transactionManifest{Version: 1, Changes: make([]transactionChange, 0, len(changes))}
	for i, change := range changes {
		entry := transactionChange{Path: change.Path, AfterExists: !change.Delete}
		parent, name, closeParent, err := openTarget(boardRoot, change.Path, false)
		if err == nil {
			before, exists, mode, err := readRegular(parent, name)
			closeParent()
			if err != nil {
				return err
			}
			if exists {
				entry.BeforeExists = true
				entry.BeforeHash = hash(before)
				entry.BeforeMode = uint32(mode.Perm())
				entry.BeforeFile = fmt.Sprintf("before-%06d", i)
				if err := writeSynced(imagesRoot, entry.BeforeFile, before, 0o600); err != nil {
					return err
				}
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if !change.Delete {
			entry.AfterHash = hash(change.Data)
			entry.AfterFile = fmt.Sprintf("after-%06d", i)
			if err := writeSynced(imagesRoot, entry.AfterFile, change.Data, 0o600); err != nil {
				return err
			}
		}
		manifest.Changes = append(manifest.Changes, entry)
	}
	if err := syncRootDirectory(imagesRoot); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := writeSynced(journalRoot, "manifest.tmp", append(encoded, '\n'), 0o600); err != nil {
		return err
	}
	if err := journalRoot.Rename("manifest.tmp", "manifest.json"); err != nil {
		return err
	}
	if err := syncRootDirectory(journalRoot); err != nil {
		return err
	}
	return syncRootDirectory(boardRoot)
}

func applyJournalChange(boardRoot, imagesRoot *os.Root, change transactionChange) error {
	rel, err := safeRelative(change.Path)
	if err != nil {
		return err
	}
	parent, name, closeParent, err := openTarget(boardRoot, rel, false)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	var current []byte
	var currentExists bool
	var mode fs.FileMode
	if err == nil {
		current, currentExists, mode, err = readRegular(parent, name)
		if err != nil {
			closeParent()
			return err
		}
	} else {
		parent = nil
	}
	if matches(current, currentExists, change.AfterHash, change.AfterExists) {
		if closeParent != nil {
			closeParent()
		}
		return nil
	}
	if !matches(current, currentExists, change.BeforeHash, change.BeforeExists) {
		if closeParent != nil {
			closeParent()
		}
		return errors.New("current file matches neither the recorded old nor new content; leaving it untouched")
	}
	if !change.AfterExists {
		if currentExists {
			removeErr := parent.Remove(name)
			if removeErr == nil {
				removeErr = syncRootDirectory(parent)
			}
			closeParent()
			return removeErr
		}
		if closeParent != nil {
			closeParent()
		}
		return nil
	}
	content, exists, _, err := readRegular(imagesRoot, change.AfterFile)
	if err != nil {
		if closeParent != nil {
			closeParent()
		}
		return fmt.Errorf("read staged content: %w", err)
	}
	if !exists {
		if closeParent != nil {
			closeParent()
		}
		return errors.New("staged content is missing")
	}
	if hash(content) != change.AfterHash {
		if closeParent != nil {
			closeParent()
		}
		return errors.New("staged content hash does not match transaction manifest")
	}
	if closeParent != nil {
		closeParent()
	}
	if mode == 0 {
		mode = fs.FileMode(change.BeforeMode)
	}
	if mode == 0 {
		mode = 0o644
	}
	parent, name, closeParent, err = openTarget(boardRoot, rel, true)
	if err != nil {
		return err
	}
	defer closeParent()
	return writeAtomic(parent, name, content, mode)
}

func openTarget(boardRoot *os.Root, rel string, createParents bool) (*os.Root, string, func(), error) {
	if rel == "board.md" {
		return boardRoot, "board.md", func() {}, nil
	}
	parts := strings.Split(rel, "/")
	if len(parts) != 3 || parts[0] != "tasks" {
		return nil, "", nil, fmt.Errorf("invalid transaction path %q", rel)
	}
	tasksRoot, err := openOrCreateDirectory(boardRoot, parts[0], createParents)
	if err != nil {
		return nil, "", nil, err
	}
	stateRoot, err := openOrCreateDirectory(tasksRoot, parts[1], createParents)
	if err != nil {
		_ = tasksRoot.Close()
		return nil, "", nil, err
	}
	return stateRoot, parts[2], func() {
		_ = stateRoot.Close()
		_ = tasksRoot.Close()
	}, nil
}

func openOrCreateDirectory(parent *os.Root, name string, create bool) (*os.Root, error) {
	info, err := parent.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) && create {
		if err := parent.Mkdir(name, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		info, err = parent.Lstat(name)
	}
	if err != nil {
		return nil, err
	}
	if !realDirectory(info) {
		return nil, fmt.Errorf("refusing to traverse non-directory or symlink path %s", name)
	}
	return openCheckedDirectory(parent, name, info)
}

func openCheckedDirectory(parent *os.Root, name string, expected os.FileInfo) (*os.Root, error) {
	child, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	directory, err := child.Open(".")
	if err != nil {
		_ = child.Close()
		return nil, err
	}
	actual, statErr := directory.Stat()
	closeErr := directory.Close()
	if statErr != nil || closeErr != nil || !os.SameFile(expected, actual) {
		_ = child.Close()
		if statErr != nil {
			return nil, statErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		return nil, fmt.Errorf("directory changed while opening")
	}
	return child, nil
}

func readRegular(root *os.Root, name string) ([]byte, bool, fs.FileMode, error) {
	info, err := root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, 0, nil
	}
	if err != nil {
		return nil, false, 0, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, false, 0, fmt.Errorf("refusing to operate on non-regular file %s", name)
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, false, 0, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, false, 0, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return nil, false, 0, fmt.Errorf("refusing to operate on changed or non-regular file %s", name)
	}
	data, err := io.ReadAll(file)
	return data, true, opened.Mode().Perm(), err
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

func writeAtomic(parent *os.Root, destination string, data []byte, mode fs.FileMode) error {
	temp, file, err := createTemp(parent, ".tuck-write-", mode)
	if err != nil {
		return err
	}
	defer parent.Remove(temp)
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := parent.Rename(temp, destination); err != nil {
		return err
	}
	return syncRootDirectory(parent)
}

func writeSynced(root *os.Root, name string, data []byte, mode fs.FileMode) error {
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
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

func createTemp(root *os.Root, prefix string, mode fs.FileMode) (string, *os.File, error) {
	for attempt := 0; attempt < 100; attempt++ {
		var random [12]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", nil, err
		}
		name := prefix + hex.EncodeToString(random[:])
		file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode.Perm())
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", nil, err
		}
		if err := file.Chmod(mode.Perm()); err != nil {
			_ = file.Close()
			_ = root.Remove(name)
			return "", nil, err
		}
		return name, file, nil
	}
	return "", nil, fmt.Errorf("unable to create temporary file")
}

func cleanupJournal(root *os.Root) error {
	if err := root.RemoveAll(transactionDirectory); err != nil {
		return err
	}
	return syncRootDirectory(root)
}

func removeWriteTemps(directory *os.Root) error {
	entries, err := readRootDirectory(directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".tuck-write-") {
			continue
		}
		info, err := directory.Lstat(entry.Name())
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to remove non-regular temporary file %s", entry.Name())
		}
		if err := directory.Remove(entry.Name()); err != nil {
			return err
		}
	}
	return syncRootDirectory(directory)
}

func readRootDirectory(root *os.Root) ([]os.DirEntry, error) {
	directory, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	return directory.ReadDir(-1)
}

func syncRootDirectory(root *os.Root) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func realDirectory(info os.FileInfo) bool {
	return info != nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0
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
	if clean == "board.md" {
		return clean, nil
	}
	parts := strings.Split(clean, "/")
	if len(parts) != 3 || parts[0] != "tasks" || !validTaskState(parts[1]) || parts[2] == "." || filepath.Ext(parts[2]) != ".md" {
		return "", fmt.Errorf("transaction path %q must name board.md or a task Markdown file", path)
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
