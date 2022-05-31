//go:build windows

package grab

import (
	"syscall"
)

func CreateHideFile(filePath string) error {
	filenameW, err := syscall.UTF16PtrFromString(filePath)
	if err != nil {
		return err
	}
	err = syscall.SetFileAttributes(filenameW, syscall.FILE_ATTRIBUTE_HIDDEN)
	if err != nil {
		return err
	}
	return nil
}
