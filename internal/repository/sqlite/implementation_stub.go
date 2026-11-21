//go:build no_sqlite

package sqlite

import (
	"database/sql"
	"errors"

	_ "github.com/mattn/go-sqlite3"
)

const impl = ImplementationNone

func open(dsn string, cfg *Cfg) (*sql.DB, error) {
	return nil, errors.New("no default sqlite3 implementation available in this build")
}

func addParams(dsn string, cfg *Cfg) string {
	return dsn
}

func IsUniqueConstraintError(err error) bool {
	return false
}

func IsForeignConstraintError(err error) bool {
	return false
}
