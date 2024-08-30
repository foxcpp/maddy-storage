package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/foxcpp/maddy-storage/internal/repository/sqlcommon"
	"gorm.io/gorm"
)

type Cfg struct {
	SlowLogThreshold time.Duration
}

type DB struct {
	db *gorm.DB
}

func New(path string, cfg Cfg) (DB, error) {
	db, err := gorm.Open(open(path), &gorm.Config{
		Logger: sqlcommon.GormLogger{
			SlowThreshold: cfg.SlowLogThreshold,
		},
	})
	if err != nil {
		return DB{}, err
	}

	ret := DB{db: db}

	if err := ret.migrationsUp(context.Background()); err != nil {
		return DB{}, err
	}

	return ret, nil
}

func NewMemory(cfg Cfg) (DB, error) {
	db, err := gorm.Open(open(":memory:"), &gorm.Config{
		Logger: sqlcommon.GormLogger{
			SlowThreshold: cfg.SlowLogThreshold,
		},
	})
	if err != nil {
		return DB{}, err
	}

	sqlDB, err := db.DB()
	if err != nil {
		return DB{}, err
	}
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetMaxOpenConns(1)

	ret := DB{db: db}

	if err := ret.migrationsUp(context.Background()); err != nil {
		return DB{}, err
	}

	return ret, nil
}

func (db DB) Tx(ctx context.Context, readOnly bool, fn func(tx DB) error) error {
	return db.Gorm(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(DB{db: tx})
	}, &sql.TxOptions{
		ReadOnly: readOnly,
	})
}

func (db DB) Gorm(ctx context.Context) *gorm.DB {
	return db.db.WithContext(ctx)
}

func (db DB) SQL() (*sql.DB, error) {
	return db.db.DB()
}

func (db DB) Close() error {
	sqlDB, err := db.SQL()
	if err != nil {
		return nil
	}
	return sqlDB.Close()
}
