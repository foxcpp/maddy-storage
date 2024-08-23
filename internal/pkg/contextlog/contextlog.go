package contextlog

import (
	"context"

	"go.uber.org/zap"
)

type loggerKey struct{}

var loggerKeyVal loggerKey

func WithLogger(ctx context.Context, l *zap.Logger) context.Context {
	return context.WithValue(ctx, loggerKeyVal, l)
}

func FromContext(ctx context.Context) *zap.Logger {
	val := ctx.Value(loggerKeyVal)
	if val == nil {
		return zap.L()
	}
	return val.(*zap.Logger)
}
