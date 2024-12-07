// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.BSD file.
//
// Copied with following modifications from
// github.com/emersion/go-message/textproto:
// * Boundary validity checks removed.
// * Header is not explicitly written for each part.

package mimeutils

import (
	"errors"
	"fmt"
	"io"
)

// A MultipartWriter generates multipart messages.
type MultipartWriter struct {
	w        io.Writer
	boundary string
	lastpart *part
}

// NewMultipartWriter returns a new multipart Writer with a random boundary,
// writing to w.
func NewMultipartWriter(w io.Writer, boundary string) *MultipartWriter {
	return &MultipartWriter{
		w:        w,
		boundary: boundary,
	}
}

// Boundary returns the Writer's boundary.
func (w *MultipartWriter) Boundary() string {
	return w.boundary
}

// CreatePart creates a new multipart section with the provided
// header. The header and the body of the part should be written to the returned
// Writer. After calling CreatePart, any previous part may no longer
// be written to.
func (w *MultipartWriter) CreatePart() (io.Writer, error) {
	if w.lastpart != nil {
		if err := w.lastpart.close(); err != nil {
			return nil, err
		}
	}
	if w.lastpart != nil {
		fmt.Fprintf(w.w, "\r\n--%s\r\n", w.boundary)
	} else {
		fmt.Fprintf(w.w, "--%s\r\n", w.boundary)
	}

	p := &part{
		mw: w,
	}
	w.lastpart = p
	return p, nil
}

// Close finishes the multipart message and writes the trailing
// boundary end line to the output.
func (w *MultipartWriter) Close() error {
	if w.lastpart != nil {
		if err := w.lastpart.close(); err != nil {
			return err
		}
		w.lastpart = nil
	}
	_, err := fmt.Fprintf(w.w, "\r\n--%s--\r\n", w.boundary)
	return err
}

type part struct {
	mw     *MultipartWriter
	closed bool
	we     error // last error that occurred writing
}

func (p *part) close() error {
	p.closed = true
	return p.we
}

func (p *part) Write(d []byte) (n int, err error) {
	if p.closed {
		return 0, errors.New("multipart: can't write to finished part")
	}
	n, err = p.mw.w.Write(d)
	if err != nil {
		p.we = err
	}
	return
}
