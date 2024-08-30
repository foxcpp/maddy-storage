//go:build !cgo

package sqlite

import (
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func open(path string) gorm.Dialector {
	dsn := path + "?_pragma=foreign_keys(1)"
	return sqlite.Open(dsn)
}
