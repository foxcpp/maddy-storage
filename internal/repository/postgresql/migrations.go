package postgresql

import (
	"context"
	"embed"
	"github.com/pressly/goose/v3"
	"io/fs"
)

//go:embed migrations/*.sql
var migrations embed.FS

func (db DB) migrationsUp(ctx context.Context) error {
	sqlDB, err := db.SQL()
	if err != nil {
		return err
	}

	migrationsFolder, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return err
	}

	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrationsFolder)
	if err != nil {
		return err
	}

	_, err = p.Up(ctx)
	return err
}
