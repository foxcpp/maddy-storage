package sqlcommon

import (
	"context"

	"github.com/foxcpp/maddy-storage/internal/pkg/contextlib"
	"go.uber.org/zap"
)

type GooseLogger struct {
	Ctx context.Context
}

func (g GooseLogger) zap(ctx context.Context) *zap.Logger {
	return contextlib.Logger(ctx).
		With(zap.String("component", "goose")).
		WithOptions(zap.AddCallerSkip(3))
}

func (g GooseLogger) Fatalf(format string, v ...any) {
	g.zap(g.Ctx).Sugar().Fatalf(format, v...)
}

func (g GooseLogger) Printf(format string, v ...any) {
	g.zap(g.Ctx).Sugar().Infof(format, v...)
}

func (g GooseLogger) NeedsVerbose() bool {
	return g.zap(g.Ctx).Level() == zap.DebugLevel
}
