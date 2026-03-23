package imap2

import (
	"runtime/trace"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/pkg/contextlib"
	"go.uber.org/zap"
)

func (s *session) Expunge(w *imapserver.ExpungeWriter, uids *imap.UIDSet) error {
	ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Expunge")
	defer task.End()

	if s.mbox.ReadOnly {
		// FIXME: Temporary work-around as go-imap calls Expunge in handleUnselect
		// event for read-only mailboxes.
		return nil
		//return &imap.Error{
		//	Type: imap.StatusResponseTypeNo,
		//	Code: imap.ResponseCodeClientBug,
		//	Text: "Cannot EXPUNGE in a read-only mailbox",
		//}
	}

	log := s.log.WithLazy(
		zap.String("imap_command", "EXPUNGE"),
		zap.Any("imap_uids", uids),
		zap.Stringer("folder_id", s.mbox.FolderID),
	)
	ctx = contextlib.WithLogger(ctx, log)

	var uidsRange folder.Range
	if uids != nil {
		var err error
		uidsRange, err = s.mbox.idsAsRange(uids)
		if err != nil {
			return s.asIMAPError(err)
		}
	}

	uidsRange.At = s.mbox.At
	uidsRange.DeletesAt = s.mbox.DeletesAt
	_, err := s.b.messages.Delete(ctx, s.accountID, s.mbox.FolderID, true, uidsRange, false)
	if err != nil {
		return s.asIMAPError(err)
	}

	// Expunges will be sent by Poll call after Expunge, this might include other messages
	// deleted in other sessions.

	if err := s.b.messages.CleanDeleted(ctx, s.accountID, s.mbox.FolderID); err != nil {
		log.Error("failed to clean dangling messages", zap.Error(err))
	}

	return nil
}
