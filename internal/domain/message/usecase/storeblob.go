package messageusecase

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"runtime/trace"
	"strconv"
	"time"

	"github.com/foxcpp/maddy-storage/internal/pkg/contextlib"
	"github.com/foxcpp/maddy-storage/internal/pkg/storeerrors"
	"github.com/oklog/ulid/v2"
	"go.uber.org/zap"
)

func (uc *Usecase) tempBuffer(ctx context.Context, accountID ulid.ULID, size int64, mime io.Reader) (b Buffer, err error) {
	defer trace.StartRegion(ctx, "maddy-storage/message.usecase.tempBuffer").End()

	log := contextlib.Logger(ctx)

	if size <= uc.cfg.MemoryBufferMaxSize {
		buf := bytes.Buffer{}
		buf.Grow(int(size))

		if _, err := io.Copy(&buf, mime); err != nil {
			return nil, fmt.Errorf("copy into memory: %w", err)
		}
		log.Debug("message temporary buffer is in memory", zap.Int64("size", size))
		return memoryBuffer{buf: buf, len: buf.Len()}, nil
	}

	key := accountID.String() + "_temp_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	w, err := uc.tempStore.Create(ctx, key)
	if err != nil {
		return nil, storeerrors.InternalError{Reason: fmt.Errorf("create temp buffer: %w", err)}
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
		return nil, fmt.Errorf("copy into temp buffer: %w", err)
	}

	log.Debug("message temporary buffer in temp store", zap.Int64("size", size), zap.String("key", key))

	return storeBuffer{
		len:      int(size),
		store:    uc.tempStore,
		storeKey: key,
	}, nil
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

func (uc *Usecase) storePartBlob(
	ctx context.Context, accountID, msgID, partID ulid.ULID,
	from io.Reader, countLines bool,
) (storedBlob, error) {
	log := contextlib.Logger(ctx)

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

func (uc *Usecase) storeExternalBlob(
	ctx context.Context, accountID, msgID, partID ulid.ULID,
	from io.Reader, countLines bool,
) (storedBlob, error) {
	log := contextlib.Logger(ctx)

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
