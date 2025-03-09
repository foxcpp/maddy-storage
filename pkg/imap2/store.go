package imap2

import (
	"runtime/trace"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	messageusecase "github.com/foxcpp/maddy-storage/internal/domain/message/usecase"
	"github.com/foxcpp/maddy-storage/internal/pkg/contextlog"
	"go.uber.org/zap"
)

func flagsAsStringList(flags []imap.Flag) []string {
	res := make([]string, 0, len(flags))
	for _, flag := range flags {
		res = append(res, string(flag))
	}
	return res
}

func stringListAsFlags(flags []string) []imap.Flag {
	res := make([]imap.Flag, 0, len(flags))
	for _, flag := range flags {
		res = append(res, imap.Flag(flag))
	}
	return res
}

func (s *session) Store(w *imapserver.FetchWriter, numSet imap.NumSet, flags *imap.StoreFlags, options *imap.StoreOptions) error {
	ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Store")
	defer task.End()

	if s.mbox.ReadOnly {
		return &imap.Error{
			Type: imap.StatusResponseTypeNo,
			Code: imap.ResponseCodeClientBug,
			Text: "Cannot STORE in a read-only mailbox",
		}
	}

	ctx = contextlog.WithLogger(ctx, s.log.WithLazy(
		zap.String("imap_command", "STORE"),
		zap.Stringer("imap_numset", numSet),
		zap.Stringer("folder_id", s.mbox.FolderID)))

	if options.UnchangedSince != 0 && !s.mbox.CondStoreActive {
		s.mbox.CondStoreActive = true
	}

	numIDs, err := s.mbox.idsAsRange(numSet)
	if err != nil {
		return s.asIMAPError(err)
	}
	var modSeqLe folder.ModSeq
	if options.UnchangedSince != 0 {
		modSeqLe = folder.ModSeq(options.UnchangedSince)
	}

	var (
		upds []messageusecase.UpdatedMessage
	)
	switch flags.Op {
	case imap.StoreFlagsDel:
		upds, err = s.b.messages.DeleteFlags(ctx, s.accountID, s.mbox.FolderID, numIDs,
			flagsAsStringList(flags.Flags), modSeqLe, true)
	case imap.StoreFlagsAdd:
		upds, err = s.b.messages.AddFlags(ctx, s.accountID, s.mbox.FolderID, numIDs,
			flagsAsStringList(flags.Flags), modSeqLe, true)
	case imap.StoreFlagsSet:
		upds, err = s.b.messages.SetFlags(ctx, s.accountID, s.mbox.FolderID, numIDs,
			flagsAsStringList(flags.Flags), modSeqLe, true)
	default:
		panic("unknown STORE operation")
	}
	if err != nil {
		return s.asIMAPError(err)
	}

	for _, upd := range upds {
		needUpdate := false
		if upd.PreviousAt > s.mbox.At {
			// Message changed between last synchronization
			// produce update anyway.
			needUpdate = true
		} else {
			needUpdate = !flags.Silent
		}

		if !needUpdate {
			continue
		}

		// Prevent Poll, Idle from duplicating our update
		// TODO: Consider just always sending UID and relying on
		// Poll to generate updates.
		s.mbox.SkipFlagUpdateUntil[upd.UID] = upd.At

		msgW := w.CreateMessage(upd.Seq)
		if !numIDs.SeqNum {
			msgW.WriteUID(imap.UID(upd.UID))
		}
		msgW.WriteFlags(stringListAsFlags(upd.Flags))
		// TODO: Write ModSeq
		if err := msgW.Close(); err != nil {
			return s.asIMAPError(err)
		}
	}

	return nil
}
