package mimeutils

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/emersion/go-message/textproto"
)

func SkipHeader(r *bufio.Reader) error {
	for {
		line, err := r.ReadSlice('\n')
		if err != nil {
			return fmt.Errorf("SkipHeader: %w", err)
		}
		// If line is empty (message uses LF delim) or contains only CR (messages uses CRLF delim)
		if len(line) == 0 || (len(line) == 1 || line[0] == '\r') {
			break
		}
	}
	return nil
}

func HeaderCopy(r *bufio.Reader, to io.Writer) error {
	for {
		line, err := r.ReadSlice('\n')
		if err != nil {
			return fmt.Errorf("HeaderCopy: %w", err)
		}
		if _, err := to.Write(line); err != nil {
			return fmt.Errorf("HeaderCopy: %w", err)
		}
		if len(line) == 0 || (len(line) == 1 || line[0] == '\r') {
			break
		}
	}
	return nil
}

func FilterHeaderCopyParsed(parsed textproto.Header, to io.Writer, include []string, exclude []string) error {
	iter := parsed.Fields()
	for iter.Next() {
		if include != nil {
			included := false
			for _, key := range include {
				if strings.EqualFold(iter.Key(), key) {
					included = true
					break
				}
			}
			if !included {
				iter.Del()
				continue
			}
		}
		if exclude != nil {
			excluded := false
			for _, key := range exclude {
				if strings.EqualFold(iter.Key(), key) {
					excluded = true
					break
				}
			}
			if excluded {
				iter.Del()
				continue
			}
		}
	}

	return textproto.WriteHeader(to, parsed)
}

func FilterHeaderCopy(r *bufio.Reader, to io.Writer, include []string, exclude []string) error {
	if len(include) == 0 && len(exclude) == 0 {
		// Simple copy, copy lines until we reach empty line.
		return HeaderCopy(r, to)
	}

	parsed, err := textproto.ReadHeader(r)
	if err != nil {
		return err
	}
	return FilterHeaderCopyParsed(parsed, to, include, exclude)
}

func IsMultipart(h textproto.Header) bool {
	return strings.HasPrefix(strings.ToLower(h.Get("Content-Type")), "multipart/")
}

func HasNestedRFC822(h textproto.Header) bool {
	return strings.EqualFold(h.Get("Content-Type"), "message/rfc822") ||
		strings.EqualFold(h.Get("Content-Type"), "message/global")
}
