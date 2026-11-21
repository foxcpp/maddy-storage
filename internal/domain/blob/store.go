package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
)

var ErrNotFound = errors.New("no such object")

type Store interface {
	Create(ctx context.Context, path string) (io.WriteCloser, error)
	Open(ctx context.Context, path string) (io.ReadCloser, error)
	Delete(ctx context.Context, path string) error
}

type StubStore struct{}

func (s StubStore) Create(ctx context.Context, path string) (io.WriteCloser, error) {
	return nil, fmt.Errorf("stub store used")
}

func (s StubStore) Open(ctx context.Context, path string) (io.ReadCloser, error) {
	return nil, fmt.Errorf("stub store used")
}

func (s StubStore) Delete(ctx context.Context, path string) error {
	return fmt.Errorf("stub store used")
}
