package imap2

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type IMAPLogger struct {
	Zap   *zap.Logger
	Level zapcore.Level
}

func (i IMAPLogger) Write(p []byte) (n int, err error) {
	i.Zap.WithOptions(zap.AddCallerSkip(1)).Log(i.Level, string(p))
	return len(p), nil
}

func (i IMAPLogger) Printf(format string, args ...interface{}) {
	i.Zap.WithOptions(zap.AddCallerSkip(1)).Sugar().Logf(i.Level, format, args...)
}
