package messageusecase

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"runtime/trace"
	"time"

	"github.com/emersion/go-message/textproto"
	"github.com/foxcpp/maddy-storage/internal/domain/blob"
	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/domain/message"
	"github.com/foxcpp/maddy-storage/internal/pkg/contextlib"
	"github.com/foxcpp/maddy-storage/internal/pkg/storeerrors"
	"github.com/oklog/ulid/v2"
	"go.uber.org/zap"
)

var ErrTooManyNestedParts = errors.New("message: too deeply nested multipart")

type memReader struct{ *bytes.Reader }

func (memReader) Close() error { return nil }

type Buffer interface {
	Open(ctx context.Context) (io.ReadCloser, error)
	ReadBytes(ctx context.Context) ([]byte, error)
	Len() int
	Close() error
}

type memoryBuffer struct {
	buf bytes.Buffer
	len int
}

func (b memoryBuffer) Open(ctx context.Context) (io.ReadCloser, error) {
	return memReader{Reader: bytes.NewReader(b.buf.Bytes())}, nil
}

func (b memoryBuffer) ReadBytes(ctx context.Context) ([]byte, error) {
	return b.buf.Bytes(), nil
}

func (b memoryBuffer) Len() int {
	return b.len
}

func (b memoryBuffer) Close() error {
	return nil
}

type storeBuffer struct {
	len      int
	store    blob.Store
	storeKey string
}

func (b storeBuffer) Open(ctx context.Context) (io.ReadCloser, error) {
	return b.store.Open(ctx, b.storeKey)
}

func (b storeBuffer) ReadBytes(ctx context.Context) ([]byte, error) {
	r, err := b.store.Open(ctx, b.storeKey)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

func (b storeBuffer) Len() int {
	return b.len
}

func (b storeBuffer) Close() error {
	return b.store.Delete(context.Background(), b.storeKey)
}

func (uc *Usecase) CreateMessage(
	ctx context.Context,
	accountID ulid.ULID, folderPath string,
	date time.Time, flags []string,
	size int64, mime io.Reader,
) (*CreateData, error) {
	defer trace.StartRegion(ctx, "maddy-storage/message.usecase.CreateMessage").End()

	newMsg, err := uc.PrepareMessage(ctx, accountID, date, flags, size, mime)
	if err != nil {
		return nil, fmt.Errorf("prepare message: %w", err)
	}

	targetFolder, err := uc.folderRepo.GetByPath(ctx, accountID, folderPath)
	if err != nil {
		if derr := uc.RemoveDanglingParts(ctx, newMsg); derr != nil {
			contextlib.Logger(ctx).Error("RemoveDanglingParts failed", zap.Error(derr))
		}
		return nil, fmt.Errorf("get folder by path %v: %w", folderPath, err)
	}

	createData, err := uc.AddMessageToFolder(ctx, accountID, newMsg, targetFolder)
	if err != nil {
		if derr := uc.RemoveDanglingParts(ctx, newMsg); derr != nil {
			contextlib.Logger(ctx).Error("RemoveDanglingParts failed", zap.Error(derr))
		}
		return nil, fmt.Errorf("add message to folder: %w", err)
	}

	return createData, nil
}

// PrepareMessage will save parts, will not save message, ModSeq is not
// initialized in returned value. accountID is used to generate
// temporary identifiers - the resulting parts objects can be
// saved for multiple accounts just fine.
func (uc *Usecase) PrepareMessage(
	ctx context.Context,
	accountID ulid.ULID,
	date time.Time, flags []string,
	size int64, mime io.Reader,
) (*message.NewMsg, error) {
	defer trace.StartRegion(ctx, "maddy-storage/message.usecase.PrepareMessage").End()

	buff, err := uc.tempBuffer(ctx, accountID, size, mime)
	if err != nil {
		return nil, fmt.Errorf("buffer msg: %w", err)
	}
	defer func(buff Buffer) {
		err := buff.Close()
		if err != nil {
			contextlib.Logger(ctx).Error("failed to delete temporary buffer", zap.Error(err))
		}
	}(buff)

	return uc.PrepareMessageBuffered(
		ctx,
		accountID,
		date, flags,
		buff,
	)
}

func (uc *Usecase) PrepareMessageBuffered(
	ctx context.Context,
	accountID ulid.ULID,
	date time.Time, flags []string,
	buffer Buffer,
) (*message.NewMsg, error) {
	defer trace.StartRegion(ctx, "maddy-storage/message.usecase.PrepareMessageBuffered").End()

	msgID := ulid.Make()
	log := contextlib.Logger(ctx).With(zap.Stringer("message_id", msgID))
	ctx = contextlib.WithLogger(ctx, log)

	r, err := buffer.Open(ctx)
	if err != nil {
		return nil, storeerrors.InternalError{Reason: fmt.Errorf("open msg buffer: %w", err)}
	}
	defer r.Close()

	parts, err := uc.storeParts(ctx, accountID, msgID, bufio.NewReader(r))
	if err != nil {
		return nil, fmt.Errorf("PrepareMessage: %w", err)
	}

	contextlib.Logger(ctx).Info("stored message parts",
		zap.Stringer("msg_id", msgID),
		zap.Int("parts_count", len(parts)),
		zap.Int("size", buffer.Len()),
	)

	return &message.NewMsg{
		ID:      msgID,
		Date:    date,
		Flags:   flags,
		Content: &message.ContentData{},
		Parts:   parts,
	}, nil
}

func (uc *Usecase) RemoveDanglingParts(ctx context.Context, m *message.NewMsg) error {
	defer trace.StartRegion(ctx, "maddy-storage/message.usecase.RemoveDanglingParts").End()

	log := contextlib.Logger(ctx)

	for _, p := range m.Parts {
		log.Debug("deleting nested part",
			zap.Stringer("nested_part_id", p.ID),
			zap.Stringer("nested_part_path", p.Path))

		if derr := uc.deleteNewPart(ctx, &p); derr != nil {
			log.Error("failed to delete new part", zap.Error(derr))
		}
	}

	return nil
}

func (uc *Usecase) AddMessageToFolder(
	ctx context.Context,
	accountID ulid.ULID,
	newMsg *message.NewMsg,
	targetFolder *folder.Folder,
) (*CreateData, error) {
	defer trace.StartRegion(ctx, "maddy-storage/message.usecase.AddMessageToFolder").End()

	modSeq, err := uc.imapRepo.NextModSeq(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("modseq: %w", err)
	}
	newMsg.ModSeq = modSeq

	msg, err := message.New(newMsg, contextlib.GetAdditionalMeta(ctx))
	if err != nil {
		return nil, fmt.Errorf("create message object: %v", err)
	}

	if err = uc.msgRepo.Create(ctx, *msg); err != nil {
		return nil, err
	}

	imapFolder, err := uc.imapRepo.GetIMAPFolder(ctx, targetFolder.ID)
	if err != nil {
		return nil, fmt.Errorf("get imap folder by id %v: %w", targetFolder.ID, err)
	}

	uids, err := uc.imapRepo.NextUID(ctx, targetFolder.ID, 1)
	if err != nil {
		return nil, fmt.Errorf("nextuid: %w", err)
	}

	entry := folder.NewEntry(targetFolder.ID, msg.ID, uids[0], msg.ModSeq, time.Now())
	if err := uc.folderRepo.CreateEntry(ctx, entry); err != nil {
		return nil, fmt.Errorf("create folder entry: %w", err)
	}

	contextlib.Logger(ctx).Info("added message to folder",
		zap.Stringer("folder_id", targetFolder.ID), zap.Stringer("msg_id", msg.ID),
		zap.Uint32("imap_uid", entry.IMAPUID), zap.Uint32("size", msg.TotalSize))

	return &CreateData{
		Folder: targetFolder,
		IMAP:   &imapFolder,
		Entry:  &entry,
		Msg:    msg,
	}, nil
}

func (uc *Usecase) bufferStoreMessage(ctx context.Context, accountID ulid.ULID, modSeq folder.ModSeq, date time.Time, flags []string, size int64, mime io.Reader) (*message.Msg, error) {
	msgID := ulid.Make()
	log := contextlib.Logger(ctx).With(zap.Stringer("message_id", msgID))
	ctx = contextlib.WithLogger(ctx, log)

	buff, err := uc.tempBuffer(ctx, accountID, size, mime)
	if err != nil {
		return nil, fmt.Errorf("buffer msg: %w", err)
	}
	defer func(buff Buffer) {
		err := buff.Close()
		if err != nil {
			log.Error("failed to delete temporary buffer", zap.Error(err))
		}
	}(buff)

	r, err := buff.Open(ctx)
	if err != nil {
		return nil, storeerrors.InternalError{Reason: fmt.Errorf("open msg buffer: %w", err)}
	}
	defer r.Close()

	bufR := bufio.NewReader(r)
	parts, err := uc.storeParts(ctx, accountID, msgID, bufR)
	if err != nil {
		log.Error("failed to store as message tree, will try storing as raw", zap.Error(err))
		part, err := uc.storeRawPart(ctx, accountID, msgID, buff)
		if err != nil {
			return nil, fmt.Errorf("store raw part: %w", err)
		}
		parts = []message.NewPart{*part}
	}

	msg, err := message.New(&message.NewMsg{
		ID:      msgID,
		ModSeq:  modSeq,
		Date:    date,
		Flags:   flags,
		Content: &message.ContentData{},
		Parts:   parts,
	}, contextlib.GetAdditionalMeta(ctx))
	if err != nil {
		return nil, fmt.Errorf("create message object: %v", err)
	}

	if err = uc.msgRepo.Create(ctx, *msg); err != nil {
		return nil, err
	}

	return msg, nil
}

func (uc *Usecase) storeRawPart(
	ctx context.Context, accountID ulid.ULID,
	msgID ulid.ULID, tempBuf Buffer,
) (part *message.NewPart, err error) {
	log := contextlib.Logger(ctx)

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

	return &message.NewPart{
		ID:   msgID,
		Path: message.Path{1},
		Content: &message.ContentPartData{
			ContentSize: blobSize,
		},
		InlineBlob: inline,
		ExternalID: blobID,
	}, nil
}

func (uc *Usecase) storeParts(ctx context.Context, accountID ulid.ULID, msgID ulid.ULID, reader *bufio.Reader) ([]message.NewPart, error) {
	log := contextlib.Logger(ctx)

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

	return parts, nil
}

func (uc *Usecase) storeMessage(ctx context.Context, accountID ulid.ULID, modSeq folder.ModSeq, msgID ulid.ULID, date time.Time, flags []string, reader *bufio.Reader) (msg *message.Msg, err error) {
	log := contextlib.Logger(ctx)

	parts, err := uc.storeParts(ctx, accountID, msgID, reader)

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
