package imap2

import (
	"math"
	"runtime/trace"

	"github.com/emersion/go-imap/v2"
	"github.com/foxcpp/maddy-storage/internal/pkg/contextlib"
	"go.uber.org/zap"
)

func (s *session) Append(mailbox string, r imap.LiteralReader, options *imap.AppendOptions) (*imap.AppendData, error) {
	ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Append")
	defer task.End()

	if s.mbox.ReadOnly {
		return nil, &imap.Error{
			Type: imap.StatusResponseTypeNo,
			Code: imap.ResponseCodeClientBug,
			Text: "Cannot APPEND in a read-only mailbox",
		}
	}

	appendLimit := s.AppendLimit()
	if appendLimit != 0 && (r.Size() > math.MaxUint32 || uint32(r.Size()) >= appendLimit) {
		return nil, &imap.Error{
			Type: imap.StatusResponseTypeNo,
			Code: imap.ResponseCodeLimit,
			Text: "APPENDLIMIT exceeded",
		}
	}

	log := s.log.WithLazy(
		zap.String("imap_command", "APPEND"),
		zap.String("imap_mailbox", mailbox),
		zap.Int64("imap_size", r.Size()))
	ctx = contextlib.WithLogger(ctx, log)

	flags := make([]string, len(options.Flags))
	for i, flag := range options.Flags {
		flags[i] = string(flag)
	}

	createdData, err := s.b.messages.CreateMessage(
		ctx, s.accountID, mailbox,
		options.Time, flags,
		r.Size(), r)
	if err != nil {
		return nil, s.asIMAPError(err)
	}

	if err := s.b.recents.AddRecent(
		ctx,
		createdData.Folder.ID,
		imap.UID(createdData.Entry.IMAPUID),
		createdData.Entry.CreatedAtModSeq,
	); err != nil {
		log.Error("failed to add recent entries", zap.Error(err))
	}

	return &imap.AppendData{
		UID:         imap.UID(createdData.Entry.IMAPUID),
		UIDValidity: createdData.IMAP.UIDValidity,
	}, nil
}
