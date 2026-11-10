package sqlite

import (
	"context"
	"embed"
	"io/fs"

	"github.com/foxcpp/maddy-storage/internal/repository/sqlcommon"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrations embed.FS

func (db DB) createGooseProvider(ctx context.Context) (*goose.Provider, error) {
	migrationsFolder, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return nil, err
	}

	log := sqlcommon.GooseLogger{Ctx: ctx}

	return goose.NewProvider(goose.DialectSQLite3, db.db, migrationsFolder,
		goose.WithLogger(log),
		goose.WithVerbose(log.NeedsVerbose()),
	)
}

func (db DB) MigrationsUp(ctx context.Context) error {
	p, err := db.createGooseProvider(ctx)
	if err != nil {
		return err
	}

	_, err = p.Up(ctx)
	return err
}

func (db DB) MigrationsDown(ctx context.Context) error {
	p, err := db.createGooseProvider(ctx)
	if err != nil {
		return err
	}

	_, err = p.Down(ctx)
	return err
}
