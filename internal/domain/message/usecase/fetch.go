package messageusecase

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"runtime/trace"

	"github.com/emersion/go-message/textproto"
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
	PartMIME    PartSpecifier = 1 << iota // MIME part header, invalid for root part.
	PartHeader                            // RFC822 header, valid for root part or message/*.
	PartBody                              // Text body of the message.
	PartNone    = 0
	PartDefault = PartHeader | PartBody
)

func (p PartSpecifier) Includes(other PartSpecifier) bool {
	return p&other != 0
}

func (uc *Usecase) Fetch(ctx context.Context,
	accountID, folderID ulid.ULID,
	ids folder.Range, modSeqGt folder.ModSeq, returnSeq bool,
) ([]FetchedMessage, error) {
	defer trace.StartRegion(ctx, "maddy-storage/message.usecase.Fetch").End()

	entries, err := uc.folderRepo.GetEntryByRange(ctx, folderID, ids, returnSeq)
	if err != nil {
		return nil, err
	}

	if len(entries) == 0 {
		return nil, nil
	}

	entByMsgID := make(map[ulid.ULID]folder.Entry, len(entries))
	msgIDs := make([]ulid.ULID, 0, len(entries))
	for _, ent := range entries {
		entByMsgID[ent.MsgID] = ent
		msgIDs = append(msgIDs, ent.MsgID)
	}

	msgs, err := uc.msgRepo.GetByIDs(ctx, modSeqGt, msgIDs...)
	if err != nil {
		return nil, err
	}

	fetched := make([]FetchedMessage, 0, len(msgs))
	for _, m := range msgs {
		fetched = append(fetched, FetchedMessage{
			Msg:   m,
			Entry: entByMsgID[m.ID],
		})
	}

	return fetched, nil
}

type WriteOptions struct {
	DecodeCTE    bool
	EncodeBinary bool

	Specifier          PartSpecifier
	HeaderFields       []string
	HeaderFieldsExcept []string

	Offset int64
	Size   int64
}

func (uc *Usecase) writeSize(
	ctx context.Context,
	msg *message.Msg, path message.Path,
	opts WriteOptions,
) (uint32, error) {
	var headerBuf bytes.Buffer
	err := uc.WritePart(ctx, msg, path, &headerBuf, opts)
	if err != nil {
		return 0, fmt.Errorf("failed to write part %v: %w", path, err)
	}

	return uint32(headerBuf.Len()), nil
}

func (uc *Usecase) PartSize(
	ctx context.Context,
	msg *message.Msg, path message.Path,
	opts WriteOptions,
) (uint32, error) {
	defer trace.StartRegion(ctx, "maddy-storage/message.usecase.PartSize").End()

	if opts.DecodeCTE || opts.HeaderFieldsExcept != nil || opts.HeaderFields != nil {
		return uc.writeSize(ctx, msg, path, opts)
	}

	rootPart := msg.FindPart(path)
	if rootPart == nil {
		return 0, nil
	}

	if opts.EncodeBinary && rootPart.Content.Encoding == "binary" {
		return uc.writeSize(ctx, msg, path, opts)
	}

	size := uint32(0)
	if rootPart.Content.IsMIMEPart && opts.Specifier.Includes(PartMIME) {
		size += rootPart.Content.HeaderSize
	}

	if (!rootPart.Content.IsMIMEPart || rootPart.IsNestedMessage()) && opts.Specifier.Includes(PartHeader) {
		if !rootPart.Content.IsMIMEPart {
			size += rootPart.Content.HeaderSize
		} else if rootPart.IsNestedMessage() {
			size += rootPart.Content.Nested.HeaderSize
		}
	}

	if !opts.Specifier.Includes(PartBody) {
		return size, nil
	}

	size += rootPart.Content.MultipartSize
	// For nested messages, Content.Size includes the size of the
	// nested header that we already included above.
	if !rootPart.IsNestedMessage() {
		size += rootPart.Content.Size
	}
	for _, p := range msg.Parts {
		if p.Path.IsDescendantOf(rootPart.Path) {
			size += p.TotalSize()
		}
	}

	if opts.Offset != 0 {
		size -= uint32(opts.Offset)
	}
	if opts.Size != 0 {
		size = uint32(min(int64(size), opts.Size))
	}

	return size, nil
}

func (uc *Usecase) WritePart(
	ctx context.Context,
	msg *message.Msg, path message.Path, to io.Writer,
	opts WriteOptions,
) error {
	defer trace.StartRegion(ctx, "maddy-storage/message.usecase.WritePart").End()

	var rootPart *message.Part
	for _, p := range msg.Parts {
		if p.Path.Equals(path) {
			rootPart = &p
			break
		}
	}
	if rootPart == nil {
		return nil
	}

	if opts.Offset != 0 || opts.Size != 0 {
		to = &offsetWriter{W: to, Offset: opts.Offset, Size: opts.Size}
	}

	if err := uc.writePartTree(ctx, msg, rootPart, to, opts); err != nil {
		return fmt.Errorf("write part tree [%v]: %w", rootPart.Path, err)
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

func (o *offsetWriter) Write(p []byte) (n int, err error) {
	origLen := len(p)

	startSection := o.Offset
	endSection := o.Offset + o.Size

	pStart := startSection - o.written
	pEnd := endSection - o.written

	if pStart < 0 {
		if o.ShortCircuit {
			return 0, errNoMoreDataNeeded
		}
		return origLen, nil
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
		return origLen, nil
	}

	o.written += int64(len(p))

	_, err = o.W.Write(p)
	return origLen, err
}

func (uc *Usecase) writePartTree(
	ctx context.Context,
	msg *message.Msg, part *message.Part,
	to io.Writer, opts WriteOptions,
) error {
	r, err := uc.openPart(ctx, part)
	if err != nil {
		return fmt.Errorf("open part: %w", err)
	}
	defer r.Close()
	buffered := bufio.NewReader(r)

	if part.Content.IsMIMEPart {
		if opts.Specifier.Includes(PartMIME) {
			if opts.EncodeBinary && part.Content.Encoding == "binary" && part.Content.IsMIMEPart && !part.IsNestedMessage() {
				hdr, err := textproto.ReadHeader(buffered)
				if err != nil {
					return fmt.Errorf("read header: %w", err)
				}
				hdr.Set("Content-Transfer-Encoding", "base64")
				if err := textproto.WriteHeader(to, hdr); err != nil {
					return fmt.Errorf("write header: %w", err)
				}
			} else {
				err := mimeutils.HeaderCopy(buffered, to)
				if err != nil {
					return fmt.Errorf("mime header copy: %w", err)
				}
			}
		} else {
			if err := mimeutils.SkipHeader(buffered); err != nil {
				return fmt.Errorf("skip mime header: %w", err)
			}
		}
	}

	if !part.Content.IsMIMEPart || part.IsNestedMessage() {
		if opts.Specifier.Includes(PartHeader) {
			if opts.EncodeBinary && part.Content.Encoding == "binary" {
				hdr, err := textproto.ReadHeader(buffered)
				if err != nil {
					return fmt.Errorf("read header: %w", err)
				}
				hdr.Set("Content-Transfer-Encoding", "base64")
				err = mimeutils.FilterHeaderCopyParsed(hdr, to, opts.HeaderFields, opts.HeaderFieldsExcept)
				if err != nil {
					return fmt.Errorf("rfc822 header copy [%v, %v]: %w",
						opts.HeaderFields, opts.HeaderFieldsExcept, err)
				}
			} else {
				err := mimeutils.FilterHeaderCopy(buffered, to, opts.HeaderFields, opts.HeaderFieldsExcept)
				if err != nil {
					return fmt.Errorf("rfc822 header copy [%v, %v]: %w",
						opts.HeaderFields, opts.HeaderFieldsExcept, err)
				}
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
	if opts.DecodeCTE && opts.EncodeBinary {
		panic("opts.DecodeCTE conflicts with opts.EncodeBinary")
	}

	// Copy message body (if non-multipart) or prologue (multipart).
	if opts.DecodeCTE {
		if _, err := mimeutils.DecodedCopy(to, buffered, part.Content.Encoding); err != nil {
			if errors.Is(err, errNoMoreDataNeeded) {
				return nil
			}
			return fmt.Errorf("decoded copy: %w", err)
		}
	} else if opts.EncodeBinary && part.Content.Encoding == "binary" {
		if _, err := mimeutils.EncodedCopy(to, buffered, "base64"); err != nil {
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

	if isMultipart || (part.Content.Nested != nil && part.Content.Nested.IsMultipart()) {
		return uc.writeMultipartBody(ctx, msg, part, to, opts)
	}

	return uc.writeNestedRFC822Body(ctx, msg, part, to, opts)
}

func (uc *Usecase) writeNestedRFC822Body(ctx context.Context, msg *message.Msg, part *message.Part, to io.Writer, opts WriteOptions) error {
	// Nested RFC822 will be represented as a single child part.

	var nestedPart *message.Part
	for _, p := range msg.Parts {
		if p.Path.IsChildOf(part.Path) {
			nestedPart = &p
			break
		}
	}
	if nestedPart == nil {
		return fmt.Errorf("writeNestedRFC822Body: nested message without corresponding child part")
	}

	return uc.writePartTree(ctx, msg, nestedPart, to, WriteOptions{
		Specifier: PartHeader | PartBody,
	})
}

func (uc *Usecase) writeMultipartBody(ctx context.Context, msg *message.Msg, part *message.Part, to io.Writer, opts WriteOptions) error {
	var boundary string
	if part.Content.Nested == nil {
		boundary = part.Content.Params["boundary"]
	} else if part.Content.Nested.IsMultipart() {
		boundary = part.Content.Nested.Params["boundary"]
	}
	if boundary == "" {
		return storeerrors.InternalError{Reason: fmt.Errorf("missing boundary param for part [%v] in message %v", part.Path, msg.ID)}
	}

	mpw := mimeutils.NewMultipartWriter(to, boundary)

	for _, subPart := range msg.Parts {
		if !subPart.Path.IsChildOf(part.Path) {
			continue
		}

		pw, err := mpw.CreatePart()
		if err != nil {
			return storeerrors.InternalError{Reason: fmt.Errorf("create part: %w", err)}
		}

		err = uc.writePartTree(ctx, msg, &subPart, pw, WriteOptions{
			Specifier: PartMIME | PartHeader | PartBody,
			DecodeCTE: opts.DecodeCTE,
		})
		if err != nil {
			return fmt.Errorf("write subpart tree [%v]: %w", subPart.Path, err)
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
	r = bytes.NewReader(part.Inline)

	if part.ExternalBlobID == "" {
		return readCloser{
			Reader: r,
			Closer: io.NopCloser(nil),
		}, nil
	}

	blobR, err := uc.blobStore.Open(ctx, part.ExternalBlobID)
	if err != nil {
		return nil, storeerrors.InternalError{Reason: fmt.Errorf("writePart: failed to open blob: %w", err)}
	}
	r = io.MultiReader(r, blobR)

	return readCloser{
		Reader: r,
		Closer: blobR,
	}, nil
}
