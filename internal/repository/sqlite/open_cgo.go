//go:build cgo

package sqlite

import (
	"fmt"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func open(path string) gorm.Dialector {
	dsn := fmt.Sprintf("file:%s?cache=shared&mode=rwc&_foreign_keys=on&_journal=WAL&_busy_timeout=10000", path)
	return sqlite.Open(dsn)
}
