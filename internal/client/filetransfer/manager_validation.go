package filetransfer

import (
	"errors"
	"path"
	"strings"
)

func validateManagerRemotePath(value string) error {
	unsafeForm := value == "" || len(value) > 4096 || value[0] != '/' || value == "/"
	if unsafeForm || strings.Contains(value, "\\") || path.Clean(value) != value {
		return errors.New("file transfer remote path is invalid")
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return errors.New("file transfer remote path is invalid")
		}
	}
	return nil
}
