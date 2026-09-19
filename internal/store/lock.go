package store

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

type Lock struct{ lock *flock.Flock }

func Acquire(root Root) (*Lock, error) {
	path := ""
	if root.GitCommon != "" {
		path = filepath.Join(root.GitCommon, "weft.lock")
	} else {
		cache, err := os.UserCacheDir()
		if err != nil {
			cache = os.TempDir()
		}
		digest := sha256.Sum256([]byte(filepath.Clean(root.Path)))
		path = filepath.Join(cache, "weft", fmt.Sprintf("%x.lock", digest[:]))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	lock := flock.New(path)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	locked, err := lock.TryLockContext(ctx, 25*time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("acquire board lock: %w", err)
	}
	if !locked {
		return nil, fmt.Errorf("board is busy after 10 seconds; retry the command")
	}
	return &Lock{lock: lock}, nil
}

func (l *Lock) Close() error {
	if l == nil || l.lock == nil {
		return nil
	}
	if err := l.lock.Unlock(); err != nil {
		return fmt.Errorf("release board lock: %w", err)
	}
	return l.lock.Close()
}
