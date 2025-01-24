//go:build !cgo

package sqlite

import (
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

const impl = "modernc"

func open(path string) gorm.Dialector {
	dsn := path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)"
	return sqlite.Open(dsn)
}
