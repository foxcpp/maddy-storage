package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/foxcpp/maddy-storage/internal/pkg/contextlib"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlcommon"
	"github.com/go-jet/jet/v2/qrm"
	j "github.com/go-jet/jet/v2/sqlite"
	"go.uber.org/zap"
)

type stringerStmt struct {
	j.Statement
}

func (stmt stringerStmt) String() string {
	return stmt.DebugSql()
}

// executor is satisfied by both *sql.DB and *sql.Tx.
type executor interface {
	qrm.Executable
	qrm.Queryable
}

// executor returns the connection statements must run on. Unlike SQL(), it
// returns the *sql.Tx when db is the one passed to the Tx callback, so jet
// statements take part in the transaction.
//
// SQL() cannot be used for that: gorm resolves a *sql.Tx back to its parent
// *sql.DB there.
func (db DB) executor() executor {
	if db.tx != nil {
		return db.tx
	}
	return db.db
}

func (db DB) skipLogErr(err error) bool {
	return db.IsForeignConstraintError(err) || db.IsUniqueConstraintError(err)
}

func (db DB) logSQL(log *zap.Logger, query j.Statement, duration time.Duration, result sql.Result) {
	fields := []zap.Field{
		zap.Duration("duration", duration),
		// wrapped into stringerStmt to avoid SQL interpolation if message is never printed (e.g. debug log disabled)
		zap.Stringer("query", stringerStmt{Statement: query}),
	}

	if result != nil {
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			log.Error("error getting rows affected", zap.Error(err))
		} else {
			fields = append(fields, zap.Int("rows_affected", int(rowsAffected)))
		}
	}

	if duration > db.cfg.SlowLogThreshold {
		log.Info("slow SQL query", fields...)
	} else {
		log.Debug("SQL query", fields...)
	}
}

func (db DB) TxIsolation(ctx context.Context, readOnly bool, level sql.IsolationLevel, fn func(tx sqlcommon.Tx) error) error {
	var (
		tx  *sql.Tx
		err error
	)
	if DefaultImplementation() == ImplementationCGo {
		// For go-sqlite3 we need to explicitly start write transactions
		// as IMMEDIATE. For modernc, this is done via _txlock parameter already.

		tx, err = db.db.BeginTx(context.TODO(), &sql.TxOptions{
			Isolation: level,
			ReadOnly:  readOnly,
		})
		if err == nil && !readOnly {
			_, err = tx.Exec("ROLLBACK; BEGIN IMMEDIATE")
		}
	} else {
		tx, err = db.db.BeginTx(ctx, &sql.TxOptions{
			Isolation: level,
			ReadOnly:  readOnly,
		})
	}
	if err != nil {
		return err
	}

	// Share same Exec, Query code.
	dbTx := DB{cfg: db.cfg, tx: tx}

	ok := false
	defer func() {
		if !ok {
			if err := tx.Rollback(); err != nil {
				contextlib.Logger(ctx).Error("tx.Rollback failed", zap.Error(err))
			}
		}
	}()

	if err := fn(dbTx); err != nil {
		return err
	}

	ok = true

	return tx.Commit()
}

func (db DB) Tx(ctx context.Context, readOnly bool, fn func(tx sqlcommon.Tx) error) error {
	// sqlite supports SERIALIZABLE only anyway so the value is just placeholder.
	return db.TxIsolation(ctx, readOnly, sql.LevelReadCommitted, fn)
}

func (db DB) Exec(ctx context.Context, query j.Statement) (sql.Result, error) {
	e := db.executor()
	log := contextlib.Logger(ctx).WithOptions(zap.AddCallerSkip(1))

	start := time.Now()

	res, err := query.ExecContext(ctx, e)
	if err != nil {
		if !db.skipLogErr(err) {
			log.Error("SQL query error",
				zap.Error(err),
				zap.Stringer("query", stringerStmt{Statement: query}))
		}
		return nil, err
	}

	db.logSQL(log, query, time.Since(start), res)
	return res, nil
}

func (db DB) Query(ctx context.Context, query j.Statement, dest any) error {
	e := db.executor()
	log := contextlib.Logger(ctx).WithOptions(zap.AddCallerSkip(1))

	start := time.Now()

	err := query.QueryContext(ctx, e, dest)
	if err != nil {
		if !db.skipLogErr(err) {
			log.Error("SQL query error",
				zap.Error(err),
				zap.Stringer("query", stringerStmt{Statement: query}))
		}
		return err
	}

	db.logSQL(log, query, time.Since(start), nil)

	return nil
}
