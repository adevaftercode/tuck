//go:build windows

package store

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

type nativeLock struct {
	overlapped windows.Overlapped
}

func tryNativeLock(file *os.File, state *nativeLock) (bool, error) {
	err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &state.overlapped)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return err == nil, err
}

func unlockNativeLock(file *os.File, state *nativeLock) error {
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &state.overlapped)
}

func hasSingleLink(file *os.File) (bool, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info); err != nil {
		return false, err
	}
	return info.NumberOfLinks == 1, nil
}
