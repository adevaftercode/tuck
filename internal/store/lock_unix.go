//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package store

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

type nativeLock struct{}

func tryNativeLock(file *os.File, _ *nativeLock) (bool, error) {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return false, nil
	}
	return err == nil, err
}

func unlockNativeLock(file *os.File, _ *nativeLock) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}

func hasSingleLink(file *os.File) (bool, error) {
	var info unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &info); err != nil {
		return false, err
	}
	return info.Nlink == 1, nil
}
