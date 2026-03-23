package messageusecase

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"runtime/trace"
	"strings"

	gomessage "github.com/emersion/go-message"
	gomail "github.com/emersion/go-message/mail"
	"github.com/emersion/go-message/textproto"
	"github.com/foxcpp/maddy-storage/internal/domain/message"
	"github.com/foxcpp/maddy-storage/internal/pkg/contextlib"
	"github.com/foxcpp/maddy-storage/internal/pkg/mimeutils"
	"github.com/oklog/ulid/v2"
	"go.uber.org/zap"
)

func (uc *Usecase) storePartsTree(
	ctx context.Context,
	accountID, msgID ulid.ULID,
	path message.Path, header textproto.Header, reader *bufio.Reader,
	orderOffset int, isMIMEPart, isInDigest bool,
) (parts []message.NewPart, err error) {
	if len(path) > uc.cfg.MaxPartNesting {
		return nil, ErrTooManyNestedParts
	}

	defer trace.StartRegion(ctx, "maddy-storage/message.usecase.storePartsTree").End()

	if isInDigest || mimeutils.HasNestedRFC822(header) {
		return uc.storeRFC822(ctx, accountID, msgID, path, header, reader, orderOffset, isMIMEPart, isInDigest)
	}

	if mimeutils.IsMultipart(header) {
		return uc.storeMultipart(ctx, accountID, msgID, path, header, reader, orderOffset, isMIMEPart)
	}

	return uc.storeLeafPart(ctx, accountID, msgID, path, header, reader, orderOffset, isMIMEPart)
}

func (uc *Usecase) storeLeafPart(
	ctx context.Context,
	accountID ulid.ULID, msgID ulid.ULID,
	path message.Path,
	header textproto.Header, reader *bufio.Reader,
	orderOffset int, isMIMEpart bool,
) ([]message.NewPart, error) {
	log := contextlib.FromContext(ctx).WithLazy(zap.Stringer("part_path", path))
	ctx = contextlib.WithLogger(ctx, log)

	partID := ulid.Make()
	partData := &message.ContentPartData{
		IsMIMEPart: isMIMEpart,
	}
	uc.fillPartDataFromHeader(ctx, header, partData, false)
	if !isMIMEpart {
		uc.fillEnvelopeFromHeader(ctx, header, partData)
	}

	var headerBlob bytes.Buffer
	if err := textproto.WriteHeader(&headerBlob, header); err != nil {
		return nil, MessageFormatError{
			Reason: fmt.Errorf("write part %v header: %w", path, err),
		}
	}
	partData.HeaderSize = uint32(headerBlob.Len())
	partData.HeaderLines = int64(bytes.Count(headerBlob.Bytes(), []byte("\n")))
	log.Debug("serialized leaf (mime/rfc822) header",
		zap.Int("header_size", headerBlob.Len()),
		zap.Int("fields_count", header.Len()))

	blob, err := uc.storePartBlob(ctx, accountID, msgID, partID,
		io.MultiReader(bytes.NewReader(headerBlob.Bytes()), reader),
		true)
	if err != nil {
		return nil, fmt.Errorf("store leaf part %v blob: %w", path, err)
	}
	defer func() {
		if err != nil {
			if blob.ExternalID != "" {
				log.Debug("deleting part blob", zap.String("external_id", blob.ExternalID))
				if derr := uc.blobStore.Delete(ctx, blob.ExternalID); derr != nil {
					log.Error("failed to delete part blob", zap.Error(derr))
				}
			}
		}
	}()

	if blob.Lines == 0 && blob.Size > 0 {
		blob.Lines = 1
	}
	partData.ContentLines = int64(blob.Lines) - partData.HeaderLines
	partData.ContentSize = uint32(blob.Size) - uint32(headerBlob.Len())

	/*
		--boundary

		hello
							<--- this empty line, that, technically, is not part of the body part
								 but appears only if the message part is terminated by an empty line.
		--boundary--
	*/
	if isMIMEpart && blob.LastByte == '\n' {
		partData.MultipartLines++
	}

	log.Debug("stored leaf part",
		zap.Stringer("part_id", partID),
		zap.Int("order", orderOffset),
		zap.Any("content", partData))

	return []message.NewPart{{
		ID:         partID,
		Order:      orderOffset,
		Path:       path,
		Content:    partData,
		InlineBlob: blob.Inline,
		ExternalID: blob.ExternalID,
	}}, nil
}

func (uc *Usecase) storeMultipart(
	ctx context.Context,
	accountID ulid.ULID, msgID ulid.ULID,
	path message.Path,
	header textproto.Header, reader *bufio.Reader,
	orderOffset int, isMIMEPart bool,
) ([]message.NewPart, error) {
	log := contextlib.FromContext(ctx).WithLazy(zap.Stringer("part_path", path))
	originalCtx := ctx // To prevent part_path from being duplicated in recursive calls.
	ctx = contextlib.WithLogger(ctx, log)

	partID := ulid.Make()
	partData := &message.ContentPartData{
		IsMIMEPart: isMIMEPart,
	}
	uc.fillPartDataFromHeader(ctx, header, partData, false)
	if !isMIMEPart {
		uc.fillEnvelopeFromHeader(ctx, header, partData)
	}

	var headerBlob bytes.Buffer
	if err := textproto.WriteHeader(&headerBlob, header); err != nil {
		return nil, MessageFormatError{
			Reason: fmt.Errorf("write part %v header: %w", path, err),
		}
	}
	partData.HeaderSize = uint32(headerBlob.Len())
	partData.HeaderLines = int64(bytes.Count(headerBlob.Bytes(), []byte("\n")))
	log.Debug("serialized multipart root header",
		zap.Int("header_size", headerBlob.Len()),
		zap.Int("fields_count", header.Len()))

	boundary, ok := partData.Params["boundary"]
	if !ok {
		return nil, MessageFormatError{Reason: fmt.Errorf("missing boundary param in content-type of nested rfc822 multipart")}
	}

	// TODO: Save multipart preamble.
	blob, err := uc.storePartBlob(ctx, accountID, msgID, partID,
		bytes.NewReader(headerBlob.Bytes()), true)
	if err != nil {
		return nil, fmt.Errorf("store multipart root part %vblob: %w", path, err)
	}

	parts := []message.NewPart{{
		ID:         partID,
		Order:      orderOffset,
		Path:       path,
		Content:    partData,
		InlineBlob: blob.Inline,
		ExternalID: blob.ExternalID,
	}}

	partsCount, subparts, err := uc.storeMultipartSubparts(
		originalCtx,
		accountID, msgID, path,
		reader, boundary, orderOffset,
		log,
		strings.EqualFold(partData.Type, "multipart/digest"),
	)
	if err != nil {
		return nil, err
	}
	parts = append(parts, subparts...)

	log.Debug("stored multipart",
		zap.Stringer("part_id", partID),
		zap.Int("parts_count", partsCount),
		zap.Int("order", orderOffset),
		zap.Any("content", partData))

	partData.MultipartSize = mimeutils.MultipartOctetSize(partsCount, boundary)
	partData.MultipartLines += mimeutils.MultipartLineCount(partsCount)
	if isMIMEPart {
		// See storeLeafPart for details.
		partData.MultipartLines++
	}

	return parts, nil
}

func (uc *Usecase) storeMultipartSubparts(
	ctx context.Context,
	accountID ulid.ULID, msgID ulid.ULID, path message.Path,
	reader *bufio.Reader, boundary string, orderOffset int,
	log *zap.Logger, isDigest bool,
) (partsCount int, parts []message.NewPart, err error) {
	defer func() {
		if err != nil {
			for _, p := range parts {
				log.Debug("deleting nested part",
					zap.Stringer("nested_part_id", p.ID),
					zap.Stringer("nested_part_path", p.Path))

				if derr := uc.deleteNewPart(ctx, &p); derr != nil {
					log.Error("failed to delete new part", zap.Error(derr))
				}
			}
		}
	}()

	mr := textproto.NewMultipartReader(reader, boundary)
	var partPath message.Path
	for part, err := mr.NextPart(); err == nil; part, err = mr.NextPart() {
		if partPath == nil {
			partPath = path.FirstChild()
		} else {
			partPath = partPath.NextSibling()
		}

		childPart, err := uc.storePartsTree(ctx, accountID, msgID,
			partPath, part.Header,
			bufio.NewReader(part), orderOffset+1+len(parts),
			true, isDigest,
		)
		if err != nil {
			return 0, nil, fmt.Errorf("store multipart part %v (%v): %w", partPath, part.Header.Get("Content-Type"), err)
		}
		parts = append(parts, childPart...)
		partsCount++
	}
	return partsCount, parts, nil
}

func (uc *Usecase) storeRFC822InMIME(
	ctx context.Context,
	accountID ulid.ULID, msgID ulid.ULID,
	path message.Path,
	mimeHeader textproto.Header, reader *bufio.Reader,
	orderOffset int, isInDigest bool,
) ([]message.NewPart, error) {
	log := contextlib.FromContext(ctx).WithLazy(zap.Stringer("part_path", path))
	originalCtx := ctx // To prevent part_path from being duplicated in recursive calls.
	ctx = contextlib.WithLogger(ctx, log)

	// MIME part information.
	partID := ulid.Make()
	partData := &message.ContentPartData{
		IsMIMEPart: true,
	}
	uc.fillPartDataFromHeader(ctx, mimeHeader, partData, isInDigest)

	// MIME header.
	var mimeHeaderBlob bytes.Buffer
	if err := textproto.WriteHeader(&mimeHeaderBlob, mimeHeader); err != nil {
		return nil, MessageFormatError{
			Reason: fmt.Errorf("write part %v mime header: %w", path, err),
		}
	}
	partData.HeaderSize = uint32(mimeHeaderBlob.Len())
	partData.HeaderLines = int64(bytes.Count(mimeHeaderBlob.Bytes(), []byte("\n")))
	log.Debug("serialized mime header",
		zap.Int("header_size", mimeHeaderBlob.Len()),
		zap.Int("fields_count", mimeHeader.Len()))

	// Inner RFC822 header.
	nestedHeader, err := textproto.ReadHeader(reader)
	if err != nil {
		return nil, MessageFormatError{
			Reason: fmt.Errorf("read nested message %v header: %w", path, err),
		}
	}
	var nestedHeaderBlob bytes.Buffer

	// Nested message data.
	nestedData := &message.ContentPartData{}
	uc.fillPartDataFromHeader(ctx, nestedHeader, nestedData, false)
	uc.fillEnvelopeFromHeader(ctx, nestedHeader, nestedData)

	if err := textproto.WriteHeader(&nestedHeaderBlob, nestedHeader); err != nil {
		return nil, MessageFormatError{
			Reason: fmt.Errorf("write part %v nested rfc822 header: %w", path, err),
		}
	}
	nestedData.HeaderSize = uint32(nestedHeaderBlob.Len())
	nestedData.HeaderLines = int64(bytes.Count(nestedHeaderBlob.Bytes(), []byte("\n")))
	log.Debug("serialized nested rfc822 header",
		zap.Int("header_size", nestedHeaderBlob.Len()),
		zap.Int("fields_count", nestedHeader.Len()))

	partData.Nested = nestedData
	partData.ContentSize = nestedData.HeaderSize
	partData.ContentLines = nestedData.HeaderLines

	var body io.Reader = bytes.NewReader(nil)
	addMultipartEnd := false
	if !nestedData.IsMultipart() && !nestedData.HasNestedMessage() {
		// TODO: Handle multipart preamble here.
		body = reader
		addMultipartEnd = true
	}

	blob, err := uc.storePartBlob(ctx, accountID, msgID, partID,
		io.MultiReader(
			bytes.NewReader(mimeHeaderBlob.Bytes()),
			bytes.NewReader(nestedHeaderBlob.Bytes()),
			body,
		), true)
	if err != nil {
		return nil, fmt.Errorf("store part header %v blob: %w", path, err)
	}
	defer func() {
		if err != nil {
			if blob.ExternalID != "" {
				log.Debug("deleting part blob", zap.String("external_id", blob.ExternalID))
				if derr := uc.blobStore.Delete(ctx, blob.ExternalID); derr != nil {
					log.Error("failed to delete part blob", zap.Error(derr))
				}
			}
		}
	}()

	/*
			--foo

			From: m1@example.com
			Subject: m1

			m1 body
		  					<-- this empty line, not part of body, but of a mutlipart separator
			--foo
			X-Mime: m2 header
	*/
	if addMultipartEnd && blob.LastByte == '\n' {
		partData.MultipartLines++
	}

	nestedData.ContentSize = uint32(blob.Size) - uint32(mimeHeaderBlob.Len()) - uint32(nestedHeaderBlob.Len())
	nestedData.ContentLines += int64(blob.Lines) - partData.HeaderLines - nestedData.HeaderLines
	partData.ContentSize += nestedData.ContentSize
	partData.ContentLines += nestedData.ContentLines

	// Outer message. CT: message/rfc822.
	parts := []message.NewPart{{
		ID:         partID,
		Order:      orderOffset,
		Path:       path,
		Content:    partData,
		InlineBlob: blob.Inline,
		ExternalID: blob.ExternalID,
	}}

	if mimeutils.IsMultipart(nestedHeader) {
		boundary, ok := nestedData.Params["boundary"]
		if !ok {
			return nil, MessageFormatError{Reason: fmt.Errorf("missing boundary param in content-type of nested multipart rfc822")}
		}

		partsCount, subparts, err := uc.storeMultipartSubparts(
			originalCtx,
			accountID, msgID, path,
			reader, boundary, orderOffset,
			log,
			strings.EqualFold(partData.Type, "multipart/digest"),
		)
		if err != nil {
			return nil, err
		}
		parts = append(parts, subparts...)

		partData.MultipartSize = mimeutils.MultipartOctetSize(partsCount, boundary)
		partData.MultipartLines += mimeutils.MultipartLineCount(partsCount)

		// See storeLeafPart for details.
		partData.MultipartLines++
	} else if mimeutils.HasNestedRFC822(nestedHeader) {
		rfc822Part, err := uc.storePartsTree(originalCtx, accountID, msgID,
			path.FirstChild(), nestedHeader,
			reader, orderOffset+1, false,
			false)
		if err != nil {
			return nil, fmt.Errorf("store nested rfc822 part %v: %w", path, err)
		}
		parts = append(parts, rfc822Part...)
	}

	return parts, nil
}

func (uc *Usecase) storeRFC822(
	ctx context.Context,
	accountID ulid.ULID, msgID ulid.ULID,
	path message.Path,
	header textproto.Header, reader *bufio.Reader,
	orderOffset int, isMIMEPart, isInDigest bool,
) ([]message.NewPart, error) {
	if isMIMEPart {
		return uc.storeRFC822InMIME(
			ctx,
			accountID, msgID,
			path,
			header, reader,
			orderOffset, isInDigest,
		)
	}

	log := contextlib.FromContext(ctx).WithLazy(zap.Stringer("part_path", path))
	originalCtx := ctx // To prevent part_path from being duplicated in recursive calls.
	ctx = contextlib.WithLogger(ctx, log)

	partID := ulid.Make()
	partData := &message.ContentPartData{
		IsMIMEPart: isMIMEPart,
	}
	uc.fillPartDataFromHeader(ctx, header, partData, false)
	uc.fillEnvelopeFromHeader(ctx, header, partData)

	// Outer RFC822 header.
	var headerBlob bytes.Buffer
	if err := textproto.WriteHeader(&headerBlob, header); err != nil {
		return nil, MessageFormatError{
			Reason: fmt.Errorf("write part %v header: %w", path, err),
		}
	}
	partData.HeaderSize = uint32(headerBlob.Len())
	partData.HeaderLines = int64(bytes.Count(headerBlob.Bytes(), []byte("\n")))
	log.Debug("serialized rfc822 header",
		zap.Int("header_size", headerBlob.Len()),
		zap.Int("fields_count", header.Len()))

	// Inner RFC822 header.
	nestedHeader, err := textproto.ReadHeader(reader)
	if err != nil {
		return nil, MessageFormatError{
			Reason: fmt.Errorf("read nested message %v header: %w", path, err),
		}
	}

	blob, err := uc.storePartBlob(ctx, accountID, msgID, partID,
		bytes.NewReader(headerBlob.Bytes()), false)
	if err != nil {
		return nil, fmt.Errorf("store part header %v blob: %w", path, err)
	}
	defer func() {
		if err != nil {
			if blob.ExternalID != "" {
				log.Debug("deleting part blob", zap.String("external_id", blob.ExternalID))
				if derr := uc.blobStore.Delete(ctx, blob.ExternalID); derr != nil {
					log.Error("failed to delete part blob", zap.Error(derr))
				}
			}
		}
	}()

	// Outer message. CT: message/rfc822.
	parts := []message.NewPart{{
		ID:         partID,
		Order:      orderOffset,
		Path:       path,
		Content:    partData,
		InlineBlob: blob.Inline,
		ExternalID: blob.ExternalID,
	}}

	// Inner message. CT: whatever.
	rfc822Part, err := uc.storePartsTree(originalCtx, accountID, msgID,
		path.FirstChild(), nestedHeader,
		reader, orderOffset+1, false,
		false)
	if err != nil {
		return nil, fmt.Errorf("store nested rfc822 part %v: %w", path, err)
	}
	parts = append(parts, rfc822Part...)

	log.Debug("stored nested rfc822 part",
		zap.Stringer("part_id", partID),
		zap.Int("order", orderOffset),
		zap.Any("content", partData),
		zap.Int("subparts_count", len(parts)-1))

	return parts, nil
}

func (uc *Usecase) fillEnvelopeFromHeader(ctx context.Context, header textproto.Header, data *message.ContentPartData) {
	log := contextlib.FromContext(ctx)
	parsedHeader := gomail.Header{Header: gomessage.Header{Header: header}}

	envelope := &message.ContentEnvelope{}

	var err error

	envelope.Date, err = parsedHeader.Date()
	if err != nil {
		log.Error("failed to parse Date header", zap.Error(err))
	}
	envelope.Subject = header.Get("Subject")

	envelope.From, err = parsedHeader.AddressList("From")
	if err != nil {
		log.Error("failed to parse From header", zap.Error(err))
	}
	envelope.Sender, err = parsedHeader.AddressList("Sender")
	if err != nil {
		log.Error("failed to parse Sender header", zap.Error(err))
	}
	envelope.ReplyTo, err = parsedHeader.AddressList("Reply-To")
	if err != nil {
		log.Error("failed to parse Reply-To header", zap.Error(err))
	}
	envelope.To, err = parsedHeader.AddressList("To")
	if err != nil {
		log.Error("failed to parse To header", zap.Error(err))
	}
	envelope.Cc, err = parsedHeader.AddressList("Cc")
	if err != nil {
		log.Error("failed to parse Cc header", zap.Error(err))
	}
	envelope.Bcc, err = parsedHeader.AddressList("Bcc")
	if err != nil {
		log.Error("failed to parse Bcc header", zap.Error(err))
	}
	envelope.InReplyTo, err = parsedHeader.MsgIDList("In-Reply-To")
	if err != nil {
		log.Error("failed to parse In-Reply-To header", zap.Error(err))
	}
	envelope.MessageID, err = parsedHeader.MessageID()
	if err != nil {
		log.Error("failed to parse Message-ID header", zap.Error(err))
	}

	if len(envelope.ReplyTo) == 0 {
		envelope.ReplyTo = envelope.From
	}
	if len(envelope.Sender) == 0 {
		envelope.Sender = envelope.From
	}

	data.Envelope = envelope
}

func (uc *Usecase) fillPartDataFromHeader(
	ctx context.Context, header textproto.Header, data *message.ContentPartData,
	defaultToRFC822 bool,
) {
	log := contextlib.FromContext(ctx)

	parsedHeader := gomessage.Header{Header: header}

	var contentType string
	_, ctParams, err := parsedHeader.ContentType()
	if err != nil {
		log.Error("failed to parse message content type", zap.Error(err), zap.String("header_value", header.Get("Content-Type")))
		contentType = "application/octet-stream"
		ctParams = nil
	} else {
		// Preserve content-type case.
		if ctRaw := parsedHeader.Get("Content-Type"); ctRaw != "" {
			base, _, _ := strings.Cut(ctRaw, ";")
			contentType = strings.TrimSpace(base)
		} else if defaultToRFC822 {
			contentType = "message/rfc822"
		} else {
			contentType = "text/plain"
		}
	}

	data.Type = contentType
	data.Params = ctParams
	if len(data.Params) == 0 {
		data.Params = nil
	}

	if parsedHeader.Has("Content-Disposition") {
		disposition, dispositionParams, err := parsedHeader.ContentDisposition()
		if err != nil {
			log.Error("failed to parse message part disposition",
				zap.Error(err),
				zap.String("header_value", header.Get("Content-Disposition")))
			disposition = ""
			dispositionParams = nil
		}
		data.Disposition = &message.Disposition{
			Value:  disposition,
			Params: dispositionParams,
		}
	}

	data.Language = mimeutils.GetContentLanguage(parsedHeader)
	data.Location = parsedHeader.Get("Content-Location")
	data.ID = parsedHeader.Get("Content-ID")
	data.Description = parsedHeader.Get("Content-Description")
	data.Encoding = parsedHeader.Get("Content-Transfer-Encoding")
}

type countingWriter struct {
	Lines    int
	Size     int
	LastByte byte
}

func (c *countingWriter) Write(p []byte) (n int, err error) {
	n = len(p)
	c.Lines += bytes.Count(p, []byte("\r\n"))
	c.Size += n
	if len(p) != 0 {
		c.LastByte = p[n-1]
	}
	return
}
