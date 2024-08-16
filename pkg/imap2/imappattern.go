package imap2

import "unicode/utf8"

// Copy-pasted from regexp package.

// Bitmap used by func special to check whether a character needs to be escaped.
var specialRegexpBytes [16]byte

// special reports whether byte b needs to be escaped by QuoteMeta.
func specialInRegexp(b byte) bool {
	return b < utf8.RuneSelf && specialRegexpBytes[b%16]&(1<<(b/16)) != 0
}

func init() {
	for _, b := range []byte(`\.+*?()|[]{}^$`) {
		specialRegexpBytes[b%16] |= 1 << (b / 16)
	}
}
