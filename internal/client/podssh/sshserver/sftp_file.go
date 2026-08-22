package sshserver

import (
	"io"
	"os"
	"sync"
)

type uploadFile struct {
	*os.File

	closeRemote func() error
	once        sync.Once
	closeErr    error
}

type downloadFile struct {
	*os.File

	once sync.Once
}

func (f *downloadFile) Close() error {
	var err error
	f.once.Do(func() {
		err = f.File.Close()
		_ = os.Remove(f.Name())
	})
	return err
}

func (f *uploadFile) Close() error {
	f.once.Do(func() {
		if err := f.Sync(); err != nil {
			f.closeErr = err
		} else if err := f.closeRemote(); err != nil {
			f.closeErr = err
		}
		if err := f.File.Close(); f.closeErr == nil {
			f.closeErr = err
		}
		_ = os.Remove(f.Name())
	})
	return f.closeErr
}

type fileInfoList []os.FileInfo

func (items fileInfoList) ListAt(destination []os.FileInfo, offset int64) (int, error) {
	if offset >= int64(len(items)) {
		return 0, io.EOF
	}
	count := copy(destination, items[offset:])
	if int(offset)+count >= len(items) {
		return count, io.EOF
	}
	return count, nil
}
