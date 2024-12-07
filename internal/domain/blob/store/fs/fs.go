package storefs

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/foxcpp/maddy-storage/internal/domain/blob"
)

type directory struct {
	path string
}

func New(path string) blob.Store {
	return &directory{path: path}
}

func (d directory) Create(ctx context.Context, path string) (io.WriteCloser, error) {
	fsPath := filepath.FromSlash(path)
	if !filepath.IsLocal(fsPath) {
		return nil, fmt.Errorf("invalid blob path (not local)")
	}
	fsPath = filepath.Join(d.path, fsPath)

	if err := os.MkdirAll(filepath.Dir(fsPath), 0777); err != nil {
		return nil, fmt.Errorf("failed to create parent directory: %w", err)
	}

	return os.Create(fsPath)
}

func (d directory) Open(ctx context.Context, path string) (io.ReadCloser, error) {
	fsPath := filepath.FromSlash(path)
	if !filepath.IsLocal(fsPath) {
		return nil, fmt.Errorf("invalid blob path (not local)")
	}
	fsPath = filepath.Join(d.path, fsPath)

	return os.Open(fsPath)
}

func (d directory) Delete(ctx context.Context, path string) error {
	fsPath := filepath.FromSlash(path)
	if !filepath.IsLocal(fsPath) {
		return fmt.Errorf("invalid blob path (not local)")
	}
	fsPath = filepath.Join(d.path, fsPath)

	return os.Remove(fsPath)
}
