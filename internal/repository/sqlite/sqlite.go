package sqlite

import (
	"context"
	"database/sql"
	"time"
)

type Cfg struct {
	SlowLogThreshold time.Duration
	MigrateOnOpen    bool
}

type DB struct {
	cfg *Cfg
	db  *sql.DB

	// Populated for value passed to Tx.
	tx *sql.Tx
}

type ImplementationType string

const (
	ImplementationCGo     ImplementationType = "cgo"     // github.com/mattn/go-sqlite3
	ImplementationModernc ImplementationType = "modernc" // modernc.org/sqlite
)

func DefaultImplementation() ImplementationType {
	return impl
}

func New(path string, cfg Cfg) (DB, error) {
	db, err := open(path, &cfg)
	if err != nil {
		return DB{}, err
	}

	ret := DB{
		cfg: &cfg,
		db:  db,
	}

	if cfg.MigrateOnOpen {
		if err := ret.MigrationsUp(context.Background()); err != nil {
			return DB{}, err
		}
	}

	return ret, nil
}

func NewMemory(cfg Cfg) (DB, error) {
	db, err := open(":memory:", &cfg)
	if err != nil {
		return DB{}, err
	}

	db.SetMaxIdleConns(1)
	db.SetMaxOpenConns(1)

	ret := DB{cfg: &cfg, db: db}

	if err := ret.MigrationsUp(context.Background()); err != nil {
		return DB{}, err
	}

	return ret, nil
}

func (db DB) SQL() *sql.DB {
	return db.db
}

func (db DB) Close() error {
	return db.db.Close()
}

func (db DB) IsUniqueConstraintError(err error) bool {
	return IsUniqueConstraintError(err)
}

func (db DB) IsForeignConstraintError(err error) bool {
	return IsForeignConstraintError(err)
}
