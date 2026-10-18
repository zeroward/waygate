// Package privatefs protects application-owned secret storage. Callers must pass
// dedicated data paths, never a shared parent directory such as /tmp.
package privatefs

import (
	"fmt"
	"os"
	"path/filepath"
)

func Dir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("private directory is not a directory: %s", path)
	}
	return os.Chmod(path, 0700)
}
func File(path string) error {
	st, err := os.Lstat(path)
	if err == nil && (!st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0) {
		return fmt.Errorf("private file is not regular: %s", path)
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Chmod(0600)
}
func Write(path string, body []byte) error {
	if err := File(path); err != nil {
		return err
	}
	return os.WriteFile(path, body, 0600)
}

// Tree can also apply a configured owner during offline storage repair.
func Tree(root string, uid, gid int) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink in private storage: %s", path)
		}
		mode := os.FileMode(0600)
		if d.IsDir() {
			mode = 0700
		} else if !d.Type().IsRegular() {
			return fmt.Errorf("non-regular private file: %s", path)
		}
		if err := os.Chmod(path, mode); err != nil {
			return err
		}
		if os.Geteuid() == 0 {
			return os.Chown(path, uid, gid)
		}
		return nil
	})
}
