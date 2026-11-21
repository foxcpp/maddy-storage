//go:build turso && !no_sqlite

package sqlite

import (
	"database/sql"
	"errors"
	"strings"

	turso "turso.tech/database/tursogo"
)

const impl = ImplementationTurso

func open(dsn string, cfg *Cfg) (*sql.DB, error) {
	dsn = addParams(dsn, cfg)
	return sql.Open("turso", dsn)
}

func addParams(dsn string, cfg *Cfg) string {
	return dsn
}

func IsUniqueConstraintError(err error) bool {
	return errors.Is(err, turso.ErrTursoConstraint) && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func IsForeignConstraintError(err error) bool {
	return errors.Is(err, turso.ErrTursoConstraint)
}
