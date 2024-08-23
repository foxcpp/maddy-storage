package blob

import (
	"context"
	"errors"
	"io"
)

var ErrNotFound = errors.New("no such object")

type Store interface {
	Create(ctx context.Context, path string) (io.WriteCloser, error)
	Open(ctx context.Context, path string) (io.ReadCloser, error)
}
