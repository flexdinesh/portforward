// Package privatefs validates ownership and permissions for local control data.
package privatefs

import (
	"fmt"
	"os"
	"syscall"
)

func Dir(path string, create bool) error {
	if create {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return err
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 || !owned(info) {
		return fmt.Errorf("%s must be a private directory owned by the current user (0700)", path)
	}
	return nil
}

func Open(path string, flags int) (*os.File, error) {
	file, err := os.OpenFile(path, flags|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || !owned(info) {
		file.Close()
		return nil, fmt.Errorf("%s must be a private regular file owned by the current user (0600)", path)
	}
	return file, nil
}

func owned(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Getuid())
}
