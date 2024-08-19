package sqlite

import (
	"errors"
	"strings"

	"github.com/mattn/go-sqlite3"
	"gorm.io/gorm"
)

func IsUniqueConstraintError(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	var sqlErr sqlite3.Error
	if errors.As(err, &sqlErr) && errors.Is(sqlErr.ExtendedCode, sqlite3.ErrConstraintUnique) {
		return true
	}
	return false
}

func IsForeignConstraintError(err error) bool {
	if errors.Is(err, gorm.ErrForeignKeyViolated) {
		return true
	}
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
