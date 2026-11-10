package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/foxcpp/maddy-storage/internal/pkg/contextlib"
	j "github.com/go-jet/jet/v2/sqlite"
	"go.uber.org/zap"
)

func Timestamp(t time.Time) j.TimestampExpression {
	return j.TimestampExp(j.String(t.Format(time.RFC3339)))
}

type stringerStmt struct {
	j.Statement
}

func (stmt stringerStmt) String() string {
	return stmt.DebugSql()
}

func (db DB) Exec(ctx context.Context, query j.Statement) (sql.Result, error) {
	d, err := db.SQL()
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
	d, err := db.SQL()
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
