package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

type contentValidator func([]byte) error

func atomicWriteFile(path string, data []byte, defaultMode fs.FileMode, validate contentValidator) error {
	mode, err := existingFileMode(path, defaultMode)
	if err != nil {
		return err
	}
	temp, err := createSiblingTemp(path, mode)
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := writeAndSyncTemp(temp, data); err != nil {
		return err
	}
	if validate != nil {
		if err := validate(data); err != nil {
			return fmt.Errorf("refusing to replace %s with invalid content: %w", path, err)
		}
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	syncDirectory(filepath.Dir(path))
	return nil
}

func existingFileMode(path string, fallback fs.FileMode) (fs.FileMode, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return fallback.Perm(), nil
	}
	if err != nil {
		return 0, err
	}
	return info.Mode().Perm(), nil
}

func createSiblingTemp(path string, mode fs.FileMode) (*os.File, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	// Why: a sibling temporary file guarantees rename stays on the same
	// filesystem, which is required for atomic replacement.
	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return nil, err
	}
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		_ = os.Remove(temp.Name())
		return nil, err
	}
	return temp, nil
}

func writeAndSyncTemp(temp *os.File, data []byte) error {
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	return temp.Close()
}

func syncDirectory(path string) {
	dir, err := os.Open(path)
	if err != nil {
		return
	}
	defer dir.Close()
	// Directory fsync is not supported by every platform/filesystem, so the
	// atomic rename remains successful even when this durability hint fails.
	_ = dir.Sync()
}
