package utils

import (
	"io"
	"strings"
	"testing"
)

type writer struct {
	t      *testing.T
	prefix string
}

func (w writer) Write(p []byte) (n int, err error) {
	w.t.Log(w.prefix + ": " + strings.TrimRight(string(p), "\r\n"))
	return len(p), nil
}

func TestLogWriter(t *testing.T, prefix string) io.Writer {
	return writer{t: t, prefix: prefix}
}
