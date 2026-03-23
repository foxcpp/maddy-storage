package imap2

import (
	"runtime/trace"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/foxcpp/maddy-storage/internal/pkg/contextlib"
	"go.uber.org/zap"
)

func (s *session) Move(w *imapserver.MoveWriter, numSet imap.NumSet, dest string) error {
	ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Move")
	defer task.End()

	if s.mbox.ReadOnly {
		return &imap.Error{
			Type: imap.StatusResponseTypeNo,
			Code: imap.ResponseCodeClientBug,
			Text: "Cannot MOVE in a read-only mailbox",
		}
	}

	log := s.log.WithLazy(
		zap.String("imap_command", "MOVE"),
		zap.Stringer("imap_numset", numSet),
	)
	ctx = contextlib.WithLogger(ctx, log)

	ids, err := s.mbox.idsAsRange(numSet)
	if err != nil {
		return s.asIMAPError(err)
	}

	result, err := s.b.messages.Move(
		ctx, s.accountID, ids,
		s.mbox.FolderID, dest,
		true,
	)
	if err != nil {
		return s.asIMAPError(err)
	}

	if err := s.b.recents.AddRecentEntries(ctx, result.TargetEntries); err != nil {
		log.Error("failed to add recent entries", zap.Error(err))
	}

	sourceUIDs := imap.UIDSet{}
	for _, ent := range result.SourceEntries {
		sourceUIDs.AddNum(imap.UID(ent.IMAPUID))
	}
	targetUIDs := imap.UIDSet{}
	for _, ent := range result.TargetEntries {
		targetUIDs.AddNum(imap.UID(ent.IMAPUID))
	}

	// Expunges will be synchronized by Poll call after Move.

	err = w.WriteCopyData(&imap.CopyData{
		UIDValidity: result.TargetIMAP.UIDValidity,
		SourceUIDs:  sourceUIDs,
		DestUIDs:    targetUIDs,
	})
	if err != nil {
		return s.asIMAPError(err)
	}

	return nil
}

func (s *session) Copy(numSet imap.NumSet, dest string) (*imap.CopyData, error) {
	ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Move")
	defer task.End()

	log := s.log.WithLazy(
		zap.String("imap_command", "COPY"),
		zap.Stringer("imap_numset", numSet),
	)
	ctx = contextlib.WithLogger(ctx, log)

	ids, err := s.mbox.idsAsRange(numSet)
	if err != nil {
		return nil, s.asIMAPError(err)
	}

	result, err := s.b.messages.Copy(ctx, s.accountID, ids, s.mbox.FolderID, dest)
	if err != nil {
		return nil, s.asIMAPError(err)
	}

	if err := s.b.recents.AddRecentEntries(ctx, result.TargetEntries); err != nil {
		log.Error("failed to add recent entries", zap.Error(err))
	}

	sourceUIDs := imap.UIDSet{}
	for _, ent := range result.SourceEntries {
		sourceUIDs.AddNum(imap.UID(ent.IMAPUID))
	}
	targetUIDs := imap.UIDSet{}
	for _, ent := range result.TargetEntries {
		targetUIDs.AddNum(imap.UID(ent.IMAPUID))
	}

	return &imap.CopyData{
		UIDValidity: result.TargetIMAP.UIDValidity,
		SourceUIDs:  sourceUIDs,
		DestUIDs:    targetUIDs,
	}, nil
}
