package sqlcommon

import (
	"context"
	"database/sql"

	"gorm.io/gorm"
)

type DB interface {
	Tx(ctx context.Context, readOnly bool, fn func(tx DB) error) error
	Dialector() string
	Gorm(ctx context.Context) *gorm.DB
	SQL() (*sql.DB, error)
	Close() error
	// ModSeq generates DB-backed monotonically increasing counter used for tracking
	// changes.
	ModSeq() (uint64, error)
	IsUniqueConstraintError(err error) bool
	IsForeignConstraintError(err error) bool
}
