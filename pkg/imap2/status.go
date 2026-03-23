package imap2

import (
	"runtime/trace"

	"github.com/emersion/go-imap/v2"
	messageusecase "github.com/foxcpp/maddy-storage/internal/domain/message/usecase"
	"github.com/foxcpp/maddy-storage/internal/pkg/contextlib"
	"go.uber.org/zap"
)

func (s *session) Status(mailbox string, options *imap.StatusOptions) (*imap.StatusData, error) {
	ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Status")
	defer task.End()

	ctx = contextlib.WithLogger(ctx, s.log.WithLazy(
		zap.String("imap_command", "STATUS"),
		zap.String("imap_mailbox", mailbox),
		zap.Any("imap_opts", options)))

	info, err := s.b.messages.FetchFolderInfo(ctx, s.accountID, mailbox, messageusecase.InfoOpts{
		ReturnNamespace: options.AppendLimit,
		ReturnIMAPMeta:  true,
		CountMsgs:       options.NumMessages,
		CountDeleted:    options.NumDeleted,
		CountUnseen:     options.NumUnseen,
		CountSize:       options.Size,
		UsedFlags:       false,
		ReturnModSeq:    options.HighestModSeq,
	})
	if err != nil {
		return nil, s.asIMAPError(err)
	}

	data := &imap.StatusData{
		Mailbox: info.Folder.Path,
	}

	if options.UIDNext {
		data.UIDNext = imap.UID(info.IMAP.UIDNext)
	}
	if options.UIDValidity {
		data.UIDValidity = info.IMAP.UIDValidity
	}
	if options.AppendLimit {
		data.AppendLimit = &info.Namespace.AppendLimit
	}
	if options.NumMessages {
		data.NumMessages = &info.Msgs
	}
	if options.NumRecent {
		recentCnt, err := s.b.recents.CountRecent(ctx, info.Folder.ID)
		if err != nil {
			return nil, s.asIMAPError(err)
		}
		data.NumRecent = &recentCnt
	}
	if options.NumDeleted {
		data.NumDeleted = &info.DeletedMsgs
	}
	if options.NumUnseen {
		data.NumUnseen = &info.UnseenMsgs
	}
	if options.Size {
		data.Size = &info.Size
	}
	if options.HighestModSeq {
		data.HighestModSeq = uint64(info.MaxModSeq)
	}

	return data, nil
}
