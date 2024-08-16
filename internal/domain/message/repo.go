package message

import (
	"context"

	"github.com/foxcpp/maddy-storage/internal/pkg/storeerrors"
	"github.com/oklog/ulid/v2"
)

var ErrNotFound = storeerrors.NotExistsError{Text: "message: no such message"}

type Repo interface {
	GetByID(ctx context.Context, id ulid.ULID) (*Msg, error)
	GetByIDs(ctx context.Context, ids ...ulid.ULID) ([]Msg, error)
	Create(ctx context.Context, m ...Msg) error
	DeleteByID(ctx context.Context, id ...ulid.ULID) error
}
