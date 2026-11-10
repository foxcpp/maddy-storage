package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/foxcpp/maddy-storage/internal/pkg/contextlib"
	"github.com/go-jet/jet/v2/qrm"
	j "github.com/go-jet/jet/v2/sqlite"
	"github.com/oklog/ulid/v2"
	"go.uber.org/zap"
)

func Timestamp(t time.Time) j.TimestampExpression {
	return j.TimestampExp(j.String(t.Format(time.RFC3339)))
}

func ULID(id ulid.ULID) j.BlobExpression {
	return j.Blob(id[:])
}

func ULIDList(ids []ulid.ULID) []j.Expression {
	res := make([]j.Expression, len(ids))
	for i, id := range ids {
		res[i] = ULID(id)
	}
	return res
}

// LikeEscape builds "col LIKE pattern ESCAPE escape", jet has no native ESCAPE support.
func LikeEscape(col j.Column, pattern, escape string) j.BoolExpression {
	return j.RawBool(
		"("+col.TableName()+"."+col.Name()+" LIKE #pattern ESCAPE '"+escape+"')",
		j.RawArgs{"#pattern": pattern},
	)
}

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
func (db DB) executor() (executor, error) {
	pool := db.db.ConnPool
	if db.db.Statement != nil && db.db.Statement.ConnPool != nil {
		pool = db.db.Statement.ConnPool
	}

	e, ok := pool.(executor)
	if !ok {
		return nil, fmt.Errorf("unsupported connection pool type: %T", pool)
	}
	return e, nil
}

func (db DB) Exec(ctx context.Context, query j.Statement) (sql.Result, error) {
	d, err := db.executor()
	if err != nil {
		return nil, err
	}

	l := contextlib.Logger(ctx).WithOptions(zap.AddCallerSkip(1))

	start := time.Now()

	res, err := query.ExecContext(ctx, d)
	if err != nil {
		l.Error("SQL query error",
			zap.Error(err),
			zap.Stringer("query", stringerStmt{Statement: query}))
		return nil, err
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		l.Error("SQL query affected error", zap.Error(err))
	}

	l.Debug("SQL query (jet)",
		zap.Duration("duration", time.Since(start)),
		zap.Int64("rows_affected", rowsAffected),
		zap.Stringer("query", stringerStmt{Statement: query}))

	return res, nil
}

func (db DB) Query(ctx context.Context, query j.Statement, dest any) error {
	d, err := db.executor()
	if err != nil {
		return err
	}

	l := contextlib.Logger(ctx).WithOptions(zap.AddCallerSkip(1))

	start := time.Now()

	err = query.QueryContext(ctx, d, dest)
	if err != nil {
		l.Error("SQL query error",
			zap.Error(err),
			zap.Stringer("query", stringerStmt{Statement: query}))
		return err
	}

	l.Debug("SQL query (jet)",
		zap.Duration("duration", time.Since(start)),
		zap.Stringer("query", stringerStmt{Statement: query}))

	return nil
}
