//go:build cgo

package sqlite

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/mattn/go-sqlite3"
	_ "github.com/mattn/go-sqlite3"
)

const impl = ImplementationCGo

func open(dsn string, cfg *Cfg) (*sql.DB, error) {
	dsn = addParams(dsn, cfg)
	return sql.Open("sqlite3", dsn)
}

func addParams(dsn string, cfg *Cfg) string {
	if !strings.HasPrefix(dsn, "file:") {
		dsn = "file:" + dsn
	}
	if !strings.Contains(dsn, "?") {
		dsn += "?"
	} else {
		dsn += "&"
	}

	dsn += "_fk=ON&_auto_vacuum=FULL&_journal_mode=WAL&_sync=NORMAL&_busy_timeout=10000"

	return dsn
}

func IsUniqueConstraintError(err error) bool {
	var sqlErr sqlite3.Error
	if errors.As(err, &sqlErr) && errors.Is(sqlErr.ExtendedCode, sqlite3.ErrConstraintUnique) {
		return true
	}
	return false
}

func IsForeignConstraintError(err error) bool {
	var sqlErr sqlite3.Error
	if errors.As(err, &sqlErr) {
		if errors.Is(sqlErr.ExtendedCode, sqlite3.ErrConstraintForeignKey) {
			return true
		}
		if errors.Is(sqlErr.ExtendedCode, sqlite3.ErrConstraintTrigger) && strings.Contains(sqlErr.Error(), "FOREIGN KEY") {
			return true
		}
		return false
	}
	return false
}
