package messageusecase

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	gomessage "github.com/emersion/go-message"
	gomail "github.com/emersion/go-message/mail"
	"github.com/emersion/go-message/textproto"
	"github.com/foxcpp/maddy-storage/internal/domain/blob"
	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/domain/message"
	"github.com/foxcpp/maddy-storage/internal/pkg/contextlog"
	"github.com/foxcpp/maddy-storage/internal/pkg/mimeutils"
	"github.com/foxcpp/maddy-storage/internal/pkg/storeerrors"
	"github.com/oklog/ulid/v2"
	"go.uber.org/zap"
)

type memReader struct{ *bytes.Reader }

func (memReader) Close() error { return nil }

type buffer struct {
	buf      bytes.Buffer
	len      int
	store    blob.Store
	storeKey string
}

func (b buffer) Open(ctx context.Context) (io.ReadCloser, error) {
	if b.storeKey == "" {
		return memReader{Reader: bytes.NewReader(b.buf.Bytes())}, nil
	}
	return b.store.Open(ctx, b.storeKey)
}

func (b buffer) ReadBytes(ctx context.Context) ([]byte, error) {
	if b.storeKey == "" {
		return b.buf.Bytes(), nil
	}

	r, err := b.store.Open(ctx, b.storeKey)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

func (b buffer) Len() int { return b.len }

func (b buffer) Close() error {
	if b.storeKey == "" {
		return nil
	}
	return b.store.Delete(context.Background(), b.storeKey)
}

func (uc *Usecase) CreateMessage(
	ctx context.Context,
	accountID ulid.ULID, folderPath string,
	date time.Time, flags []string,
	size int64, mime io.Reader,
) (*CreateData, error) {
	targetFolder, err := uc.folderRepo.GetByPath(ctx, accountID, folderPath)
	if err != nil {
		return nil, fmt.Errorf("get folder by path %v: %w", folderPath, err)
	}

	uids, err := uc.folderRepo.NextUID(ctx, targetFolder.ID_, 1)
	if err != nil {
		return nil, fmt.Errorf("nextuid: %w", err)
	}

	msg, err := uc.bufferStoreMessage(ctx, accountID, date, flags, size, mime)
	if err != nil {
		return nil, fmt.Errorf("bufferstore: %w", err)
	}

	entry := folder.NewEntry(targetFolder.ID_, msg.ID_, uids[0])
	if err := uc.folderRepo.CreateEntry(ctx, entry); err != nil {
		return nil, fmt.Errorf("create folder entry: %w", err)
	}

	return &CreateData{
		Folder: targetFolder,
		Entry:  &entry,
		Msg:    msg,
	}, nil
}

func (uc *Usecase) tempBuffer(ctx context.Context, accountID ulid.ULID, size int64, mime io.Reader) (b buffer, err error) {
	log := contextlog.FromContext(ctx)

	if size <= uc.cfg.MemoryBufferMaxSize {
		buf := bytes.Buffer{}
		buf.Grow(int(size))

		if _, err := io.Copy(&buf, mime); err != nil {
			return buffer{}, fmt.Errorf("copy into memory: %w", err)
		}
		log.Debug("message temporary buffer is in memory", zap.Int64("size", size))
		return buffer{buf: buf}, nil
	}

	key := accountID.String() + "_temp_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	w, err := uc.tempStore.Create(ctx, key)
	if err != nil {
		return buffer{}, storeerrors.InternalError{Reason: fmt.Errorf("create temp buffer: %w", err)}
	}
	defer func() {
		cerr := w.Close()
		if cerr != nil {
			// May result in partial write with some implementations.
			err = storeerrors.InternalError{
				Reason: fmt.Errorf("tempBuffer: failed to close temp store buffer: %w", err),
			}
		}

		if err != nil {
			derr := uc.tempStore.Delete(ctx, key)
			if derr != nil {
				log.Error("failed to delete temporary store buffer", zap.Error(err), zap.String("key", key))
			}
		}
	}()

	size, err = io.Copy(w, mime)
	if err != nil {
		// no wrapping, there may be both internal (store write) and client I/O errors.
		return buffer{}, fmt.Errorf("copy into temp buffer: %w", err)
	}

	log.Debug("message temporary buffer in temp store", zap.Int64("size", size), zap.String("key", key))

	return buffer{
		len:      int(size),
		store:    uc.tempStore,
		storeKey: key,
	}, nil
}

func (uc *Usecase) bufferStoreMessage(
	ctx context.Context,
	accountID ulid.ULID,
	date time.Time, flags []string,
	size int64, mime io.Reader,
) (*message.Msg, error) {
	msgID := ulid.Make()
	log := contextlog.FromContext(ctx).With(zap.Stringer("message_id", msgID))
	ctx = contextlog.WithLogger(ctx, log)

	buff, err := uc.tempBuffer(ctx, accountID, size, mime)
	if err != nil {
		return nil, fmt.Errorf("buffer msg: %w", err)
	}
	defer func(buff buffer) {
		err := buff.Close()
		if err != nil {
			log.Error("failed to delete temporary buffer", zap.Error(err), zap.String("key", buff.storeKey))
		}
	}(buff)

	r, err := buff.Open(ctx)
	if err != nil {
		return nil, storeerrors.InternalError{Reason: fmt.Errorf("open msg buffer: %w", err)}
	}
	defer r.Close()

	bufR := bufio.NewReader(r)
	msg, err := uc.storeMessage(ctx, accountID, msgID, date, flags, bufR)
	if err != nil {
		log.Error("failed to store as message tree, will try storing as raw", zap.Error(err), zap.String("key", buff.storeKey))
		return uc.storeRawMessage(ctx, accountID, msgID, date, flags, buff)
	}

	return msg, nil
}

func (uc *Usecase) createPartExternalBlob(ctx context.Context, accountID, msgID, partID ulid.ULID) (path string, wc io.WriteCloser, err error) {
	key := accountID.String() + "_" + msgID.String() + "_" + partID.String()
	wc, err = uc.blobStore.Create(ctx, key)
	return key, wc, err
}

type storedBlob struct {
	Inline     []byte
	ExternalID string
	Size       int
}

func (uc *Usecase) storePartBlob(ctx context.Context, accountID, msgID, partID ulid.ULID, from io.Reader) (storedBlob, error) {
	log := contextlog.FromContext(ctx)

	// First try to read up to N bytes.
	initial := make([]byte, uc.cfg.InlineMaxPartSize)
	actualSize, err := io.ReadFull(from, initial)
	if err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			log.Debug("keeping the part in RAM (got EOF)", zap.Int("actual_size", actualSize))
			return storedBlob{Inline: initial[:actualSize], Size: actualSize}, nil
		}
		if err == io.EOF {
			// Special case: message with empty body.
			return storedBlob{Inline: []byte{}}, nil
		}
		// Some I/O error happened, bail out.
		return storedBlob{}, fmt.Errorf("read initial size: %w", err)
	}
	if actualSize < uc.cfg.InlineMaxPartSize {
		log.Debug("keeping the part in RAM (got short read)", zap.Int("actual_size", actualSize))
		return storedBlob{Inline: initial[:actualSize], Size: actualSize}, nil
	}

	size, externalID, err := uc.storeExternalPart(ctx, accountID, msgID, partID, io.MultiReader(bytes.NewReader(initial[:actualSize]), from))
	if err != nil {
		return storedBlob{}, storeerrors.InternalError{
			Reason: fmt.Errorf("external store: %w", err),
		}
	}
	return storedBlob{ExternalID: externalID, Size: int(size)}, nil
}

func (uc *Usecase) storeExternalPart(ctx context.Context, accountID, msgID, partID ulid.ULID, from io.Reader) (size int64, externalID string, err error) {
	log := contextlog.FromContext(ctx)

	externalID, wc, err := uc.createPartExternalBlob(ctx, accountID, msgID, partID)
	if err != nil {
		return 0, "", storeerrors.InternalError{
			Reason: fmt.Errorf("create part %v in external store: %w", partID, err),
		}
	}

	defer func(wc io.WriteCloser) {
		cerr := wc.Close()
		if cerr != nil {
			err = fmt.Errorf("close external store: %w", cerr)
		}

		if err != nil {
			if derr := uc.blobStore.Delete(ctx, externalID); derr != nil {
				log.Error("delete external store", zap.Error(derr), zap.String("key", externalID))
			}
		}
	}(wc)

	size, err = io.Copy(wc, from)
	if err != nil {
		return 0, "", storeerrors.InternalError{
			Reason: fmt.Errorf("copy part %v (id=%v): %w", partID, externalID, err),
		}
	}

	return size, externalID, nil
}

func (uc *Usecase) storeRawMessage(
	ctx context.Context,
	accountID, msgID ulid.ULID,
	date time.Time, flags []string,
	tempBuf buffer,
) (msg *message.Msg, err error) {
	log := contextlog.FromContext(ctx)

	var inline []byte
	var blobID string
	var blobSize uint32
	if tempBuf.Len() <= uc.cfg.InlineMaxPartSize {
		var err error
		inline, err = tempBuf.ReadBytes(ctx)
		if err != nil {
			return nil, storeerrors.InternalError{Reason: fmt.Errorf("read tempbuf: %v", err)}
		}
		blobSize = uint32(len(inline))
	} else {
		rc, err := tempBuf.Open(ctx)
		if err != nil {
			return nil, storeerrors.InternalError{Reason: fmt.Errorf("open tempbuf: %v", err)}
		}
		defer rc.Close()

		size, path, err := uc.storeExternalPart(ctx, accountID, msgID, msgID, rc)
		if err != nil {
			return nil, storeerrors.InternalError{Reason: fmt.Errorf("store external part: %v", err)}
		}
		defer func() {
			if err != nil {
				derr := uc.blobStore.Delete(ctx, path)
				if derr != nil {
					log.Error("failed to delete blob store buffer", zap.Error(derr), zap.String("key", path))
				}
			}
		}()

		blobID = path
		blobSize = uint32(size)
	}

	msg, err = message.New(&message.NewMsg{
		ID:      msgID,
		Date:    date,
		Flags:   flags,
		Content: &message.ContentData{},
		Parts: []message.NewPart{
			{
				ID:   msgID,
				Path: message.Path{1},
				Content: &message.ContentPartData{
					Size: blobSize,
				},
				InlineBlob: inline,
				ExternalID: blobID,
			},
		},
	})
	if err != nil {
		return nil, storeerrors.InternalError{Reason: fmt.Errorf("build message: %w", err)}
	}

	if err := uc.msgRepo.Create(ctx, *msg); err != nil {
		return nil, fmt.Errorf("save created message: %w", err)
	}

	return msg, nil
}

func (uc *Usecase) storeMessage(
	ctx context.Context,
	accountID, msgID ulid.ULID,
	date time.Time, flags []string,
	reader *bufio.Reader,
) (msg *message.Msg, err error) {
	log := contextlog.FromContext(ctx)

	header, err := textproto.ReadHeader(reader)
	if err != nil {
		// TODO: Make it possible to distinguish malformed header errors (upstream issue)
		return nil, fmt.Errorf("read header: %w", err)
	}

	log.Debug("read root message header", zap.Int("fields_count", header.Len()))

	parts, err := uc.storePartsTree(ctx, accountID, msgID, message.EmptyPath(),
		header, reader, 0, false)
	if err != nil {
		return nil, fmt.Errorf("store parts tree: %w", err)
	}

	log.Debug("mime parts saved", zap.Int("parts_count", len(parts)))

	defer func() {
		if err != nil {
			for _, p := range parts {
				log.Debug("deleting part", zap.Stringer("part_id", p.ID))
				if derr := uc.deleteNewPart(ctx, &p); derr != nil {
					log.Error("failed to delete new part", zap.Error(derr))
				}
			}
		}
	}()

	msg, err = message.New(&message.NewMsg{
		ID:      msgID,
		Date:    date,
		Flags:   flags,
		Content: &message.ContentData{},
		Parts:   parts,
	})
	if err != nil {
		return nil, fmt.Errorf("create message object: %v", err)
	}

	if err = uc.msgRepo.Create(ctx, *msg); err != nil {
		return nil, err
	}

	return msg, nil
}

type MessageFormatError struct {
	Reason error
}

func (e MessageFormatError) Error() string {
	return e.Reason.Error()
}

func (e MessageFormatError) Unwrap() error {
	return e.Reason
}

func (uc *Usecase) storePartsTree(
	ctx context.Context,
	accountID, msgID ulid.ULID,
	path message.Path, header textproto.Header, reader *bufio.Reader,
	orderOffset int, isMIMEPart bool,
) (parts []message.NewPart, err error) {
	if mimeutils.HasNestedRFC822(header) {
		return uc.storeNestedRFC822(ctx, accountID, msgID, path, header, reader, orderOffset, isMIMEPart)
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
	log := contextlog.FromContext(ctx).WithLazy(zap.Stringer("part_path", path))
	ctx = contextlog.WithLogger(ctx, log)

	partID := ulid.Make()
	partData := &message.ContentPartData{}
	uc.fillPartDataFromHeader(ctx, header, partData)
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
	partData.HeaderNumLines = int64(bytes.Count(headerBlob.Bytes(), []byte("\n")))
	log.Debug("serialized leaf (mime/rfc822) header",
		zap.Int("header_size", headerBlob.Len()),
		zap.Int("fields_count", header.Len()))

	lineCounter := &countingReader{R: reader}
	reader = bufio.NewReader(lineCounter)

	blob, err := uc.storePartBlob(ctx, accountID, msgID, partID,
		io.MultiReader(bytes.NewReader(headerBlob.Bytes()), reader))
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

	if lineCounter.Lines == 0 && lineCounter.Size > 0 {
		lineCounter.Lines = 1
	}
	partData.NumLines = int64(lineCounter.Lines)
	partData.Size = uint32(blob.Size) - uint32(headerBlob.Len())

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
	log := contextlog.FromContext(ctx).WithLazy(zap.Stringer("part_path", path))
	originalCtx := ctx // To prevent part_path from being duplicated in recursive calls.
	ctx = contextlog.WithLogger(ctx, log)

	partID := ulid.Make()
	partData := &message.ContentPartData{}
	uc.fillPartDataFromHeader(ctx, header, partData)
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
	partData.HeaderNumLines = int64(bytes.Count(headerBlob.Bytes(), []byte("\n")))
	log.Debug("serialized multipart root header",
		zap.Int("header_size", headerBlob.Len()),
		zap.Int("fields_count", header.Len()))

	boundary, ok := partData.Params["boundary"]
	if !ok {
		return nil, MessageFormatError{Reason: fmt.Errorf("missing boundary param in content-type of nested rfc822 multipart")}
	}

	// TODO: Save multipart preamble.
	blob, err := uc.storePartBlob(ctx, accountID, msgID, partID, bytes.NewReader(headerBlob.Bytes()))
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

	subparts, err := uc.storeMultipartSubparts(
		originalCtx,
		accountID, msgID, path,
		reader, boundary, orderOffset,
		log)
	if err != nil {
		return nil, err
	}
	parts = append(parts, subparts...)

	log.Debug("stored multipart",
		zap.Stringer("part_id", partID),
		zap.Int("order", orderOffset),
		zap.Any("content", partData))

	return parts, nil
}

func (uc *Usecase) storeMultipartSubparts(
	ctx context.Context,
	accountID ulid.ULID, msgID ulid.ULID, path message.Path,
	reader *bufio.Reader, boundary string, orderOffset int,
	log *zap.Logger,
) (parts []message.NewPart, err error) {
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
			bufio.NewReader(part), orderOffset+1+len(parts), true)
		if err != nil {
			return nil, fmt.Errorf("store multipart part %v (%v): %w", partPath, part.Header.Get("Content-Type"), err)
		}
		parts = append(parts, childPart...)
	}
	return parts, nil
}

func (uc *Usecase) storeNestedRFC822(
	ctx context.Context,
	accountID ulid.ULID, msgID ulid.ULID,
	path message.Path,
	header textproto.Header, reader *bufio.Reader,
	orderOffset int, isMIMEPart bool,
) ([]message.NewPart, error) {
	log := contextlog.FromContext(ctx).WithLazy(zap.Stringer("part_path", path))
	originalCtx := ctx // To prevent part_path from being duplicated in recursive calls.
	ctx = contextlog.WithLogger(ctx, log)

	partID := ulid.Make()
	partData := &message.ContentPartData{}
	uc.fillPartDataFromHeader(ctx, header, partData)
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
	partData.HeaderNumLines = int64(bytes.Count(headerBlob.Bytes(), []byte("\n")))
	log.Debug("serialized rfc822 header",
		zap.Int("header_size", headerBlob.Len()),
		zap.Int("fields_count", header.Len()))

	lineCounter := &countingReader{R: reader}
	reader = bufio.NewReader(lineCounter)

	nestedHeader, err := textproto.ReadHeader(reader)
	if err != nil {
		return nil, MessageFormatError{
			Reason: fmt.Errorf("read nested message %v header: %w", path, err),
		}
	}
	var nestedHeaderBlob bytes.Buffer
	if mimeutils.IsMultipart(nestedHeader) {
		// The only case when there are two headers in part - MIME part header and RFC822 header.

		nestedData := &message.ContentPartData{}
		uc.fillPartDataFromHeader(ctx, nestedHeader, nestedData)
		uc.fillEnvelopeFromHeader(ctx, nestedHeader, nestedData)

		if err := textproto.WriteHeader(&nestedHeaderBlob, nestedHeader); err != nil {
			return nil, MessageFormatError{
				Reason: fmt.Errorf("write part %v nested rfc822 header: %w", path, err),
			}
		}
		nestedData.HeaderSize = uint32(nestedHeaderBlob.Len())
		nestedData.HeaderNumLines = int64(bytes.Count(nestedHeaderBlob.Bytes(), []byte("\n")))
		log.Debug("serialized nested rfc822 header",
			zap.Int("header_size", headerBlob.Len()),
			zap.Int("fields_count", header.Len()))

		partData.Nested = nestedData
		partData.Size = nestedData.HeaderSize
		partData.NumLines = nestedData.HeaderNumLines
	}

	blob, err := uc.storePartBlob(ctx, accountID, msgID, partID,
		io.MultiReader(
			bytes.NewReader(headerBlob.Bytes()),
			bytes.NewReader(nestedHeaderBlob.Bytes()),
		))
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

	parts := []message.NewPart{{
		ID:         partID,
		Order:      orderOffset,
		Path:       path,
		Content:    partData,
		InlineBlob: blob.Inline,
		ExternalID: blob.ExternalID,
	}}

	if mimeutils.IsMultipart(nestedHeader) {
		boundary, ok := partData.Nested.Params["boundary"]
		if !ok {
			return nil, MessageFormatError{Reason: fmt.Errorf("missing boundary param in content-type of nested rfc822 multipart")}
		}

		var subparts []message.NewPart
		subparts, err = uc.storeMultipartSubparts(
			originalCtx,
			accountID, msgID, path,
			reader, boundary, orderOffset,
			log)
		if err != nil {
			return nil, err
		}
		parts = append(parts, subparts...)
	} else {
		rfc822Part, err := uc.storePartsTree(originalCtx, accountID, msgID,
			path.FirstChild(), nestedHeader,
			reader, orderOffset+1, false)
		if err != nil {
			return nil, fmt.Errorf("store nested rfc822 part %v: %w", path, err)
		}
		parts = append(parts, rfc822Part...)
	}

	log.Debug("stored nested rfc822 part",
		zap.Stringer("part_id", partID),
		zap.Int("order", orderOffset),
		zap.Any("content", partData),
		zap.Int("subparts_count", len(parts)-1))

	return parts, nil
}

func (uc *Usecase) fillEnvelopeFromHeader(ctx context.Context, header textproto.Header, data *message.ContentPartData) {
	log := contextlog.FromContext(ctx)
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

func (uc *Usecase) fillPartDataFromHeader(ctx context.Context, header textproto.Header, data *message.ContentPartData) {
	log := contextlog.FromContext(ctx)

	parsedHeader := gomessage.Header{Header: header}

	contentType, ctParams, err := parsedHeader.ContentType()
	if err != nil {
		log.Error("failed to parse message content type", zap.Error(err), zap.String("header_value", header.Get("Content-Type")))
		contentType = "application/octet-stream"
		ctParams = nil
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

type countingReader struct {
	R     io.Reader
	Lines int
	Size  int
}

func (c *countingReader) Read(p []byte) (n int, err error) {
	n, err = c.R.Read(p)
	c.Lines += bytes.Count(p[:n], []byte("\r\n"))
	c.Size += n
	return
}
