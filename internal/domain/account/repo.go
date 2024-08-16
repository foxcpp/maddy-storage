package account

import (
	"context"
	"time"

	"github.com/foxcpp/maddy-storage/internal/pkg/storeerrors"
	"github.com/oklog/ulid/v2"
)

var ErrNotFound = storeerrors.NotExistsError{Text: "no such account"}

type Order int

const (
	OrderID Order = iota + 1
	OrderName
)

type Repo interface {
	GetAll(ctx context.Context, createdAtGt time.Time, order Order) ([]Account, error)
	GetByID(ctx context.Context, id ulid.ULID) (*Account, error)
	GetByName(ctx context.Context, name string) (*Account, error)
	Create(ctx context.Context, account *Account) error
	Delete(ctx context.Context, id ulid.ULID) error
}
