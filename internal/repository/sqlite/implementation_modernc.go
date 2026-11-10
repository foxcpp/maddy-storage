//go:build !cgo || modernc

// 'modernc' is the same as used by maddy.

package sqlite

import (
	"database/sql"
	"errors"
	"strings"

	"modernc.org/sqlite"
	_ "modernc.org/sqlite"
)

const impl = ImplementationModernc

func open(dsn string, cfg *Cfg) (*sql.DB, error) {
	dsn = addParams(dsn, cfg)
	return sql.Open("sqlite", dsn)
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

	// _txlock affects non-read-only transactions. Avoiding transaction upgrade problem
	// that results in SQLITE_BUSY:
	// https://berthub.eu/articles/posts/a-brief-post-on-sqlite3-database-locked-despite-timeout/

	// _time_format makes modernc.org default format for time.Time match mattn/go-sqlite3. Needed for compatibility.

	dsn += "_time_format=sqlite&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(10000)&_txlock=immediate"

	return dsn
}

func IsUniqueConstraintError(err error) bool {
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) && sqliteErr.Code() == (19|(8<<8)) {
		return true
	}
	return false
}

func IsForeignConstraintError(err error) bool {
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) && sqliteErr.Code() == (19|(3<<8)) {
		return true
	}
	if errors.As(err, &sqliteErr) && sqliteErr.Code() == (19|(7<<8)) && strings.Contains(sqliteErr.Error(), "FOREIGN KEY") {
		return true
	}
	return false
}
