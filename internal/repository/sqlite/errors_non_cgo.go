//go:build !cgo

package sqlite

import (
	"errors"
	"strings"

	"github.com/glebarez/go-sqlite"
	"gorm.io/gorm"
)

func IsUniqueConstraintError(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) && sqliteErr.Code() == (19|(8<<8)) {
		return true
	}
	return false
}

func IsForeignConstraintError(err error) bool {
	if errors.Is(err, gorm.ErrForeignKeyViolated) {
		return true
	}
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) && sqliteErr.Code() == (19|(3<<8)) {
		return true
	}
	if errors.As(err, &sqliteErr) && sqliteErr.Code() == (19|(7<<8)) && strings.Contains(sqliteErr.Error(), "FOREIGN KEY") {
		return true
	}
	return false
}
