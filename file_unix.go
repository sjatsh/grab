//go:build !windows

package grab

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func CreateHideFile(filePath string) error {
	dir := filepath.Dir(filePath)
	fileName := filepath.Base(filePath)
	fileName = strings.TrimPrefix(fileName, ".")
	f, err := os.OpenFile(fmt.Sprintf("%s%s.%s", dir, string(os.PathSeparator), fileName), os.O_RDONLY|os.O_CREATE|os.O_TRUNC, 0660)
	if err != nil {
		return err
	}
	return f.Close()
}
