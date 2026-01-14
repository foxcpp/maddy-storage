package storememory

import (
	"bytes"
	"context"
	"io"

	"github.com/foxcpp/maddy-storage/internal/domain/blob"
)

type Store struct {
	Blobs map[string]*bytes.Buffer
}

func New() *Store {
	return &Store{
		Blobs: make(map[string]*bytes.Buffer),
	}
}

type nopCloser struct {
	io.Writer
}

func (nopCloser) Close() error { return nil }

func (s Store) Create(_ context.Context, path string) (io.WriteCloser, error) {
	s.Blobs[path] = &bytes.Buffer{}
	return nopCloser{Writer: s.Blobs[path]}, nil
}

func (s Store) Open(_ context.Context, path string) (io.ReadCloser, error) {
	buf, ok := s.Blobs[path]
	if !ok {
		return nil, blob.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(buf.Bytes())), nil
}

func (s Store) Delete(_ context.Context, path string) error {
	delete(s.Blobs, path)
	return nil
}

func (s Store) Len() int { return len(s.Blobs) }

func (s Store) Clear() {
	for k := range s.Blobs {
		delete(s.Blobs, k)
	}
}
