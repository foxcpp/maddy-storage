package utils

import "strings"

func CRLF(s string) string {
	return strings.ReplaceAll(s, "\n", "\r\n")
}
