//go:build !windows && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package store

import (
	"errors"
	"os"
)

type nativeLock struct{}

func tryNativeLock(_ *os.File, _ *nativeLock) (bool, error) {
	return false, errors.New("board locking is unsupported on this platform")
}

func unlockNativeLock(_ *os.File, _ *nativeLock) error { return nil }

func hasSingleLink(_ *os.File) (bool, error) { return true, nil }
