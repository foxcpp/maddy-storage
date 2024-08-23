package sqlcommon

import (
	"context"
	"errors"
	"time"

	"github.com/foxcpp/maddy-storage/internal/pkg/contextlog"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type GormLogger struct {
	SlowThreshold time.Duration
}

func (g GormLogger) zap(ctx context.Context) *zap.Logger {
	return contextlog.FromContext(ctx).
		With(zap.String("component", "gorm")).
		WithOptions(zap.AddCallerSkip(3))
}

func (g GormLogger) LogMode(level logger.LogLevel) logger.Interface {
	return g
}

func (g GormLogger) Info(ctx context.Context, s string, i ...interface{}) {
	g.zap(ctx).Sugar().Infof(s, i...)
}

func (g GormLogger) Warn(ctx context.Context, s string, i ...interface{}) {
	g.zap(ctx).Sugar().Errorf(s, i...)
}

func (g GormLogger) Error(ctx context.Context, s string, i ...interface{}) {
	g.zap(ctx).Sugar().Errorf(s, i...)
}

func (g GormLogger) Trace(ctx context.Context, begin time.Time, fc func() (sql string, rowsAffected int64), err error) {
	elapsed := time.Since(begin)
	switch {
	case err != nil && !errors.Is(err, gorm.ErrRecordNotFound):
		sql, rows := fc()
		if rows == -1 {
			g.zap(ctx).Warn("failed SQL query",
				zap.String("sql", sql), zap.Duration("elapsed", elapsed), zap.Error(err))
		} else {
			g.zap(ctx).Warn("failed SQL query",
				zap.String("sql", sql), zap.Duration("elapsed", elapsed), zap.Error(err),
				zap.Int64("rows_affected", rows))
		}
	case elapsed > g.SlowThreshold && g.SlowThreshold != 0:
		sql, rows := fc()
		if rows == -1 {
			g.zap(ctx).Warn("slow SQL query",
				zap.String("sql", sql), zap.Duration("elapsed", elapsed))
		} else {
			g.zap(ctx).Warn("slow SQL query",
				zap.String("sql", sql), zap.Duration("elapsed", elapsed), zap.Int64("rows_affected", rows))
		}
	default:
		sql, rows := fc()
		if rows == -1 {
			g.zap(ctx).Debug("SQL query",
				zap.String("sql", sql), zap.Duration("elapsed", elapsed))
		} else {
			g.zap(ctx).Debug("SQL query",
				zap.String("sql", sql), zap.Duration("elapsed", elapsed), zap.Int64("rows_affected", rows))
		}
	}
}
