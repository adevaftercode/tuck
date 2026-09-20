package store

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"time"
)

const (
	lockDirectory = ".tuck"
	lockFilename  = "lock"
	lockIgnore    = "/.tuck/"
)

type Lock struct {
	file   *os.File
	native *nativeLock
}

func Acquire(root Root) (*Lock, error) {
	boardRoot, err := os.OpenRoot(root.Path)
	if err != nil {
		return nil, err
	}
	defer boardRoot.Close()

	tasksInfo, err := boardRoot.Lstat("tasks")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("missing tasks directory; run tuck init")
		}
		return nil, err
	}
	if !realDirectory(tasksInfo) {
		return nil, fmt.Errorf("tasks path is not a real directory")
	}
	tasksRoot, err := openCheckedDirectory(boardRoot, "tasks", tasksInfo)
	if err != nil {
		return nil, err
	}
	defer tasksRoot.Close()

	lockRoot, err := openOrCreateLockDirectory(tasksRoot, lockDirectory)
	if err != nil {
		return nil, fmt.Errorf("open Tuck lock directory: %w", err)
	}
	defer lockRoot.Close()

	file, err := openLockFile(lockRoot)
	if err != nil {
		return nil, fmt.Errorf("open Tuck lock file: %w", err)
	}

	state := &nativeLock{}
	deadline := time.Now().Add(10 * time.Second)
	for {
		locked, lockErr := tryNativeLock(file, state)
		if lockErr != nil {
			_ = file.Close()
			return nil, fmt.Errorf("acquire board lock: %w", lockErr)
		}
		if locked {
			break
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			_ = file.Close()
			return nil, fmt.Errorf("board is busy after 10 seconds; retry the command")
		}
		pause := 25 * time.Millisecond
		if remaining < pause {
			pause = remaining
		}
		time.Sleep(pause)
	}

	if err := ensureLockIgnored(tasksRoot); err != nil {
		_ = unlockNativeLock(file, state)
		_ = file.Close()
		return nil, fmt.Errorf("prepare ignored Tuck lock metadata: %w", err)
	}
	return &Lock{file: file, native: state}, nil
}

func (l *Lock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	file := l.file
	l.file = nil
	unlockErr := unlockNativeLock(file, l.native)
	closeErr := file.Close()
	if unlockErr != nil {
		return fmt.Errorf("release board lock: %w", unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close board lock: %w", closeErr)
	}
	return nil
}

func openOrCreateLockDirectory(parent *os.Root, name string) (*os.Root, error) {
	return openOrCreateDirectoryMode(parent, name, true, 0o700)
}

func openLockFile(root *os.Root) (*os.File, error) {
	for attempt := 0; attempt < 2; attempt++ {
		info, err := root.Lstat(lockFilename)
		flags := os.O_RDWR
		if errors.Is(err, fs.ErrNotExist) {
			flags |= os.O_CREATE | os.O_EXCL
		} else if err != nil {
			return nil, err
		} else if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%s must be a regular file", lockFilename)
		}

		file, err := root.OpenFile(lockFilename, flags, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		opened, statErr := file.Stat()
		entry, entryErr := root.Lstat(lockFilename)
		if statErr != nil || entryErr != nil || !opened.Mode().IsRegular() || !entry.Mode().IsRegular() || entry.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, entry) {
			_ = file.Close()
			if statErr != nil {
				return nil, statErr
			}
			if entryErr != nil {
				return nil, entryErr
			}
			return nil, fmt.Errorf("%s changed while opening", lockFilename)
		}
		singleLink, err := hasSingleLink(file)
		if err != nil {
			_ = file.Close()
			return nil, err
		}
		if !singleLink {
			_ = file.Close()
			return nil, fmt.Errorf("%s must not be a hard link", lockFilename)
		}
		return file, nil
	}
	return nil, fmt.Errorf("%s changed while opening", lockFilename)
}

func ensureLockIgnored(tasksRoot *os.Root) error {
	for attempt := 0; attempt < 3; attempt++ {
		data, _, _, err := readRegular(tasksRoot, ".gitignore")
		if err != nil {
			return err
		}
		if containsLockIgnore(data) {
			return nil
		}

		info, err := tasksRoot.Lstat(".gitignore")
		flags := os.O_RDWR | os.O_APPEND
		if errors.Is(err, fs.ErrNotExist) {
			flags |= os.O_CREATE | os.O_EXCL
		} else if err != nil {
			return err
		} else if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("tasks/.gitignore must be a regular file")
		}

		file, err := tasksRoot.OpenFile(".gitignore", flags, 0o644)
		if errors.Is(err, fs.ErrExist) || errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		opened, statErr := file.Stat()
		entry, entryErr := tasksRoot.Lstat(".gitignore")
		if statErr != nil || entryErr != nil || !opened.Mode().IsRegular() || !entry.Mode().IsRegular() || entry.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, entry) {
			_ = file.Close()
			if statErr != nil {
				return statErr
			}
			if entryErr != nil {
				return entryErr
			}
			return fmt.Errorf("tasks/.gitignore changed while opening")
		}
		singleLink, err := hasSingleLink(file)
		if err != nil {
			_ = file.Close()
			return err
		}
		if !singleLink {
			_ = file.Close()
			return fmt.Errorf("tasks/.gitignore must not be a hard link")
		}

		if _, err := file.Seek(0, io.SeekStart); err != nil {
			_ = file.Close()
			return err
		}
		current, err := io.ReadAll(file)
		if err != nil {
			_ = file.Close()
			return err
		}
		if containsLockIgnore(current) {
			return file.Close()
		}
		appendData := []byte(lockIgnore + "\n")
		if len(current) > 0 && current[len(current)-1] != '\n' {
			appendData = append([]byte{'\n'}, appendData...)
		}
		written, writeErr := file.Write(appendData)
		if writeErr == nil && written != len(appendData) {
			writeErr = io.ErrShortWrite
		}
		if writeErr == nil {
			writeErr = file.Sync()
		}
		closeErr := file.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
		return syncRootDirectory(tasksRoot)
	}
	return fmt.Errorf("tasks/.gitignore changed repeatedly while opening")
}

func containsLockIgnore(data []byte) bool {
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == lockIgnore {
			return true
		}
	}
	return false
}
