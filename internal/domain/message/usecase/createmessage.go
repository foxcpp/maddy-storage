package messageusecase

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"runtime/trace"
	"strconv"
	"strings"
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

var ErrTooManyNestedParts = errors.New("message: too deeply nested multipart")

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
	defer trace.StartRegion(ctx, "maddy-storage/message.usecase.CreateMessage").End()

	targetFolder, err := uc.folderRepo.GetByPath(ctx, accountID, folderPath)
	if err != nil {
		return nil, fmt.Errorf("get folder by path %v: %w", folderPath, err)
	}

	imapFolder, err := uc.imapRepo.GetIMAPFolder(ctx, targetFolder.ID)
	if err != nil {
		return nil, fmt.Errorf("get imap folder by id %v: %w", targetFolder.ID, err)
	}

	uids, err := uc.imapRepo.NextUID(ctx, targetFolder.ID, 1)
	if err != nil {
		return nil, fmt.Errorf("nextuid: %w", err)
	}

	modSeq, err := uc.imapRepo.NextModSeq(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("modseq: %w", err)
	}

	msg, err := uc.bufferStoreMessage(ctx, accountID, modSeq, date, flags, size, mime)
	if err != nil {
		return nil, fmt.Errorf("bufferstore: %w", err)
	}

	entry := folder.NewEntry(targetFolder.ID, msg.ID, uids[0], modSeq, time.Now())
	if err := uc.folderRepo.CreateEntry(ctx, entry); err != nil {
		return nil, fmt.Errorf("create folder entry: %w", err)
	}

	contextlog.FromContext(ctx).Info("created message",
		zap.Stringer("folder_id", targetFolder.ID), zap.Stringer("msg_id", msg.ID),
		zap.Uint32("imap_uid", entry.IMAPUID), zap.Uint32("size", msg.TotalSize))

	return &CreateData{
		Folder: targetFolder,
		IMAP:   &imapFolder,
		Entry:  &entry,
		Msg:    msg,
	}, nil
}

func (uc *Usecase) tempBuffer(ctx context.Context, accountID ulid.ULID, size int64, mime io.Reader) (b buffer, err error) {
	defer trace.StartRegion(ctx, "maddy-storage/message.usecase.tempBuffer").End()

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

func (uc *Usecase) bufferStoreMessage(ctx context.Context, accountID ulid.ULID, modSeq folder.ModSeq, date time.Time, flags []string, size int64, mime io.Reader) (*message.Msg, error) {
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
	msg, err := uc.storeMessage(ctx, accountID, modSeq, msgID, date, flags, bufR)
	if err != nil {
		log.Error("failed to store as message tree, will try storing as raw", zap.Error(err), zap.String("key", buff.storeKey))
		return uc.storeRawMessage(ctx, accountID, modSeq, msgID, date, flags, buff)
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

	Lines    int
	LastByte byte // populated only if countLines=true (Lines!=0)
}

func (uc *Usecase) storePartBlob(ctx context.Context, accountID, msgID, partID ulid.ULID, from io.Reader, countLines bool) (storedBlob, error) {
	log := contextlog.FromContext(ctx)

	// First try to read up to N bytes.
	initial := make([]byte, uc.cfg.InlineMaxPartSize)
	actualSize, err := io.ReadFull(from, initial)
	if err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			log.Debug("keeping the part in RAM (got EOF)", zap.Int("actual_size", actualSize))

			lines := 0
			if countLines {
				lines = bytes.Count(initial[:actualSize], []byte("\r\n"))
			}

			return storedBlob{
				Inline:   initial[:actualSize],
				Size:     actualSize,
				Lines:    lines,
				LastByte: initial[actualSize-1],
			}, nil
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

		lines := 0
		if countLines {
			lines = bytes.Count(initial[:actualSize], []byte("\r\n"))
		}

		return storedBlob{
			Inline:   initial[:actualSize],
			Size:     actualSize,
			Lines:    lines,
			LastByte: initial[actualSize-1],
		}, nil
	}

	return uc.storeExternalBlob(ctx, accountID, msgID, partID, io.MultiReader(bytes.NewReader(initial[:actualSize]), from), countLines)
}

func (uc *Usecase) storeExternalBlob(ctx context.Context, accountID, msgID, partID ulid.ULID, from io.Reader, countLines bool) (storedBlob, error) {
	log := contextlog.FromContext(ctx)

	externalID, wc, err := uc.createPartExternalBlob(ctx, accountID, msgID, partID)
	if err != nil {
		return storedBlob{}, storeerrors.InternalError{
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

	writer := io.Writer(wc)
	var lineCounter *countingWriter
	if countLines {
		lineCounter = &countingWriter{}
		writer = io.MultiWriter(lineCounter, wc)
	}

	size, err := io.Copy(writer, from)
	if err != nil {
		return storedBlob{}, storeerrors.InternalError{
			Reason: fmt.Errorf("copy part %v (id=%v): %w", partID, externalID, err),
		}
	}

	blob := storedBlob{ExternalID: externalID, Size: int(size)}
	if lineCounter != nil {
		blob.Lines = lineCounter.Lines
		blob.LastByte = lineCounter.LastByte
	}

	return blob, nil
}

func (uc *Usecase) storeRawMessage(ctx context.Context, accountID ulid.ULID, modSeq folder.ModSeq, msgID ulid.ULID, date time.Time, flags []string, tempBuf buffer) (msg *message.Msg, err error) {
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

		blob, err := uc.storeExternalBlob(ctx, accountID, msgID, msgID, rc, false)
		if err != nil {
			return nil, storeerrors.InternalError{Reason: fmt.Errorf("store external part: %v", err)}
		}
		defer func() {
			if err != nil {
				derr := uc.blobStore.Delete(ctx, blob.ExternalID)
				if derr != nil {
					log.Error("failed to delete blob store buffer", zap.Error(derr), zap.String("key", blob.ExternalID))
				}
			}
		}()

		blobID = blob.ExternalID
		blobSize = uint32(blob.Size)
	}

	msg, err = message.New(&message.NewMsg{
		ID:      msgID,
		ModSeq:  modSeq,
		Date:    date,
		Flags:   flags,
		Content: &message.ContentData{},
		Parts: []message.NewPart{
			{
				ID:   msgID,
				Path: message.Path{1},
				Content: &message.ContentPartData{
					ContentSize: blobSize,
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

func (uc *Usecase) storeMessage(ctx context.Context, accountID ulid.ULID, modSeq folder.ModSeq, msgID ulid.ULID, date time.Time, flags []string, reader *bufio.Reader) (msg *message.Msg, err error) {
	log := contextlog.FromContext(ctx)

	header, err := textproto.ReadHeader(reader)
	if err != nil {
		// TODO: Make it possible to distinguish malformed header errors (upstream issue)
		return nil, fmt.Errorf("read header: %w", err)
	}

	log.Debug("read root message header", zap.Int("fields_count", header.Len()))

	parts, err := uc.storePartsTree(ctx, accountID, msgID, message.EmptyPath(),
		header, reader, 0, false, false)
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
		ModSeq:  modSeq,
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
	log := contextlog.FromContext(ctx).WithLazy(zap.Stringer("part_path", path))
	ctx = contextlog.WithLogger(ctx, log)

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
	log := contextlog.FromContext(ctx).WithLazy(zap.Stringer("part_path", path))
	originalCtx := ctx // To prevent part_path from being duplicated in recursive calls.
	ctx = contextlog.WithLogger(ctx, log)

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
	log := contextlog.FromContext(ctx).WithLazy(zap.Stringer("part_path", path))
	originalCtx := ctx // To prevent part_path from being duplicated in recursive calls.
	ctx = contextlog.WithLogger(ctx, log)

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

	log := contextlog.FromContext(ctx).WithLazy(zap.Stringer("part_path", path))
	originalCtx := ctx // To prevent part_path from being duplicated in recursive calls.
	ctx = contextlog.WithLogger(ctx, log)

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

func (uc *Usecase) fillPartDataFromHeader(
	ctx context.Context, header textproto.Header, data *message.ContentPartData,
	defaultToRFC822 bool,
) {
	log := contextlog.FromContext(ctx)

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
