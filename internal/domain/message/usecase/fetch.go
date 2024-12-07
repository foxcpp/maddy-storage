package messageusecase

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/domain/message"
	"github.com/foxcpp/maddy-storage/internal/pkg/mimeutils"
	"github.com/foxcpp/maddy-storage/internal/pkg/storeerrors"
	"github.com/oklog/ulid/v2"
)

type FetchedMessage struct {
	Msg   message.Msg
	Entry folder.Entry
}

type PartSpecifier int

const (
	PartMIME   PartSpecifier = 1 << iota // MIME part header, invalid for root part.
	PartHeader                           // RFC822 header, valid for root part or message/*.
	PartBody                             // Text body of the message.
)

func (p PartSpecifier) Includes(other PartSpecifier) bool {
	return p&other != 0
}

func (uc *Usecase) FetchByUID(ctx context.Context,
	accountID, folderID ulid.ULID,
	uids []folder.UIDRange,
) ([]FetchedMessage, error) {
	entries, err := uc.folderRepo.GetEntryByUIDRange(ctx, folderID, uids...)
	if err != nil {
		return nil, err
	}

	if len(entries) == 0 {
		return nil, nil
	}

	entByMsgID := make(map[ulid.ULID]folder.Entry, len(entries))
	msgIDs := make([]ulid.ULID, 0, len(entries))
	for _, ent := range entries {
		entByMsgID[ent.MsgID_] = ent
		msgIDs = append(msgIDs, ent.MsgID_)
	}

	msgs, err := uc.msgRepo.GetByIDs(ctx, msgIDs...)
	if err != nil {
		return nil, err
	}

	fetched := make([]FetchedMessage, 0, len(msgs))
	for _, m := range msgs {
		fetched = append(fetched, FetchedMessage{
			Msg:   m,
			Entry: entByMsgID[m.ID_],
		})
	}

	return fetched, nil
}

type WriteOptions struct {
	DecodeCTE bool

	Specifier          PartSpecifier
	HeaderFields       []string
	HeaderFieldsExcept []string

	Offset int64
	Size   int64
}

func (uc *Usecase) WritePart(
	ctx context.Context,
	msg *message.Msg, path message.Path, to io.Writer,
	opts WriteOptions,
) error {
	var rootPart *message.Part
	var isMIMEPart bool
	for _, p := range msg.Parts_ {
		if p.Path_.Equals(path) {
			rootPart = &p
			break
		}
	}
	if rootPart == nil {
		return nil
	}
	for _, p := range msg.Parts_ {
		if rootPart.Path_.IsChildOf(p.Path_) && p.IsMultipart() {
			isMIMEPart = true
		}
	}

	if opts.Offset != 0 || opts.Size != 0 {
		to = offsetWriter{W: to, Offset: opts.Offset, Size: opts.Size}
	}

	if err := uc.writePartTree(ctx, msg, rootPart, to, opts, isMIMEPart); err != nil {
		return fmt.Errorf("write part tree [%v]: %w", rootPart.Path_, err)
	}
	return nil
}

type offsetWriter struct {
	W            io.Writer
	Offset       int64
	Size         int64
	ShortCircuit bool

	written int64
}

var errNoMoreDataNeeded = errors.New("offsetWriter finished writing necessary section")

func (o offsetWriter) Write(p []byte) (n int, err error) {
	origLen := len(p)

	startSection := o.Offset
	endSection := o.Offset + o.Size

	pStart := startSection - o.written
	pEnd := endSection - o.written

	if pStart < 0 {
		pStart = 0
	}
	if pStart > int64(len(p)) {
		pStart = int64(len(p))
	}
	if pEnd < 0 {
		if o.ShortCircuit {
			return 0, errNoMoreDataNeeded
		}
		pEnd = 0
	}
	if pEnd > int64(len(p)) {
		pEnd = int64(len(p))
	}

	p = p[pStart:pEnd]
	if len(p) == 0 {
		return 0, nil
	}

	_, err = o.W.Write(p)
	return origLen, err
}

func (uc *Usecase) writePartTree(
	ctx context.Context,
	msg *message.Msg, part *message.Part,
	to io.Writer, opts WriteOptions,
	isMIMEPart bool,
) error {
	r, err := uc.openPart(ctx, part)
	if err != nil {
		return fmt.Errorf("open part: %w", err)
	}
	defer r.Close()
	buffered := bufio.NewReader(r)

	if isMIMEPart {
		if opts.Specifier.Includes(PartMIME) {
			err := mimeutils.HeaderCopy(buffered, to)
			if err != nil {
				return fmt.Errorf("mime header copy: %w", err)
			}
		} else {
			if err := mimeutils.SkipHeader(buffered); err != nil {
				return fmt.Errorf("skip mime header: %w", err)
			}
		}
	}

	if !isMIMEPart || part.IsNestedMessage() {
		if opts.Specifier.Includes(PartHeader) {
			err := mimeutils.FilterHeaderCopy(buffered, to, opts.HeaderFields, opts.HeaderFieldsExcept)
			if err != nil {
				return fmt.Errorf("rfc822 header copy [%v, %v]: %w",
					opts.HeaderFields, opts.HeaderFieldsExcept, err)
			}
		} else {
			if err := mimeutils.SkipHeader(buffered); err != nil {
				return fmt.Errorf("rfc822 header skip: %w", err)
			}
		}
	}

	if !opts.Specifier.Includes(PartBody) {
		return nil
	}

	if err := uc.writePartBody(ctx, msg, part, to, opts, buffered); err != nil {
		return fmt.Errorf("write part body: %w", err)
	}
	return nil
}

func (uc *Usecase) writePartBody(ctx context.Context, msg *message.Msg, part *message.Part, to io.Writer, opts WriteOptions, buffered *bufio.Reader) error {
	// Copy message body (if non-multipart) or prologue (multipart).
	if opts.DecodeCTE {
		if _, err := mimeutils.DecodedCopy(to, buffered, part.Content_.Encoding); err != nil {
			if errors.Is(err, errNoMoreDataNeeded) {
				return nil
			}
			return fmt.Errorf("decoded copy: %w", err)
		}
	} else {
		if _, err := io.Copy(to, buffered); err != nil {
			if errors.Is(err, errNoMoreDataNeeded) {
				return nil
			}
			return fmt.Errorf("body copy: %w", err)
		}
	}

	isMultipart := part.IsMultipart()
	isNestedMsg := part.IsNestedMessage()

	if !isMultipart && !isNestedMsg {
		return nil
	}

	var boundary string
	if isMultipart {
		boundary = part.Content_.Params["boundary"]
	} else if isNestedMsg && part.Content_.Nested.IsMultipart() {
		boundary = part.Content_.Nested.Params["boundary"]
	}
	if boundary == "" {
		return storeerrors.InternalError{Reason: fmt.Errorf("missing boundary param for part [%v] in message %v", part.Path_, msg.ID_)}
	}

	mpw := mimeutils.NewMultipartWriter(to, boundary)

	for _, subPart := range msg.Parts_ {
		if !subPart.Path_.IsChildOf(part.Path_) {
			continue
		}

		pw, err := mpw.CreatePart()
		if err != nil {
			return storeerrors.InternalError{Reason: fmt.Errorf("create part: %w", err)}
		}

		err = uc.writePartTree(ctx, msg, &subPart, pw, WriteOptions{
			Specifier: PartMIME | PartHeader | PartBody,
			DecodeCTE: opts.DecodeCTE,
		}, isMultipart)
		if err != nil {
			return fmt.Errorf("write subpart tree [%v]: %w", subPart.Path_, err)
		}
	}

	if err := mpw.Close(); err != nil {
		return fmt.Errorf("multipart writer close: %w", err)
	}

	return nil
}

type readCloser struct {
	io.Reader
	io.Closer
}

func (uc *Usecase) openPart(ctx context.Context, part *message.Part) (io.ReadCloser, error) {
	var r io.Reader
	r = bytes.NewReader(part.Inline_)

	if part.ExternalBlobID_ == "" {
		return readCloser{
			Reader: r,
			Closer: io.NopCloser(nil),
		}, nil
	}

	blobR, err := uc.blobStore.Open(ctx, part.ExternalBlobID_)
	if err != nil {
		return nil, storeerrors.InternalError{Reason: fmt.Errorf("writePart: failed to open blob: %w", err)}
	}
	r = io.MultiReader(r, blobR)

	return readCloser{
		Reader: r,
		Closer: blobR,
	}, nil
}
