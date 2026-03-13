package kernel

import (
	"io/fs"
	"os"
	"path/filepath"
)

type FileSystem interface {
	WalkDir(root string, fn fs.WalkDirFunc) error
	ReadFile(name string) ([]byte, error)
}

type osFS struct{}

func (osFS) WalkDir(root string, fn fs.WalkDirFunc) error {
	return filepath.WalkDir(root, fn)
}

func (osFS) ReadFile(name string) ([]byte, error) {
	return os.ReadFile(name)
}
