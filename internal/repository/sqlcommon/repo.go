package sqlcommon

import (
	"context"
	"database/sql"

	j "github.com/go-jet/jet/v2/sqlite"
	"gorm.io/gorm"
)

type DB interface {
	Dialector() string

	Gorm(ctx context.Context) *gorm.DB
	SQL() (*sql.DB, error)

	Close() error

	IsUniqueConstraintError(err error) bool
	IsForeignConstraintError(err error) bool

	MigrationsUp(ctx context.Context) error
	MigrationsDown(ctx context.Context) error

	Tx(ctx context.Context, readOnly bool, fn func(tx DB) error) error
	Exec(ctx context.Context, query j.Statement) (sql.Result, error)
	Query(ctx context.Context, query j.Statement, dest any) error
}
