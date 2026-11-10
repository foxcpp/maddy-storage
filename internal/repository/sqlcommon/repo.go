package sqlcommon

import (
	"context"
	"database/sql"

	j "github.com/go-jet/jet/v2/sqlite"
)

type Tx interface {
	Exec(ctx context.Context, query j.Statement) (sql.Result, error)
	Query(ctx context.Context, query j.Statement, dest any) error
}

type DB interface {
	Close() error

	IsUniqueConstraintError(err error) bool
	IsForeignConstraintError(err error) bool

	MigrationsUp(ctx context.Context) error
	MigrationsDown(ctx context.Context) error

	Tx(ctx context.Context, readOnly bool, fn func(Tx) error) error
	Exec(ctx context.Context, query j.Statement) (sql.Result, error)
	Query(ctx context.Context, query j.Statement, dest any) error
}
