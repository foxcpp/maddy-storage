package imap2

import (
	"context"
	"errors"
	"fmt"
	"runtime/trace"
	"strconv"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/domain/folder/recent"
	"github.com/foxcpp/maddy-storage/internal/domain/message/searcher"
	"github.com/foxcpp/maddy-storage/internal/pkg/contextlib"
	"github.com/oklog/ulid/v2"
	"go.uber.org/zap"
)

var errIdleStopped = errors.New("idle stopped")

type ExpungeWriter interface {
	WriteExpunge(seqNum uint32) error
}

func (s *session) applyExpungeUpdates(ctx context.Context, w ExpungeWriter, entries []folder.EntryChange) error {
	if len(entries) == 0 {
		return nil
	}

	log := contextlib.Logger(ctx)

	needMaxUIDUpd := false
	newAt := s.mbox.DeletesAt

	for i := len(entries) - 1; i >= 0; i-- {
		ent := entries[i].Deleted
		if ent == nil {
			continue
		}

		if newAt < ent.ModSeq {
			newAt = ent.ModSeq
		}

		if _, ok := s.mbox.SkipExpunges[ent.IMAPUID]; ok {
			delete(s.mbox.SkipExpunges, ent.IMAPUID)
			continue
		}

		log.Debug("sending expunge",
			zap.Uint32("uid", ent.IMAPUID),
			zap.Uint32("seqnum", ent.SeqNum),
			zap.Uint64("modseq", uint64(ent.ModSeq)),
		)

		if ent.SeqNum == 0 {
			panic("missing SeqNum for IMAPUID " + strconv.Itoa(int(ent.IMAPUID)))
		}
		if s.mbox.Msgs == 0 {
			panic("sending expunge for empty mailbox view")
		}
		if err := w.WriteExpunge(ent.SeqNum); err != nil {
			return fmt.Errorf("failed to write expunge: %w", err)
		}

		if ent.IMAPUID == s.mbox.MaxUID {
			needMaxUIDUpd = true
		}
		s.mbox.Msgs--
	}

	if needMaxUIDUpd {
		res, err := s.b.messages.Search(ctx, s.accountID, s.mbox.FolderID, nil, searcher.Opts{
			ReturnMaxUID: true,
			At:           s.mbox.At,
			DeletesAt:    s.mbox.DeletesAt,
		})
		if err != nil {
			return fmt.Errorf("search max uid: %w", err)
		}
		s.mbox.MaxUID = res.MaxUID
	}

	s.mbox.DeletesAt = newAt

	return nil
}

func (s *session) applyOtherUpdates(ctx context.Context, w *imapserver.UpdateWriter, entries []folder.EntryChange) error {
	if len(entries) == 0 {
		return nil
	}

	log := contextlib.Logger(ctx)

	newAt := s.mbox.At
	newMaxUID := s.mbox.MaxUID

	hasNewMessages := false
	for i := len(entries) - 1; i >= 0; i-- {
		ent := entries[i]
		if ent.New == nil {
			continue
		}

		if newAt < ent.At {
			newAt = ent.At
		}
		hasNewMessages = true

		log.Debug("new message in mailbox view",
			zap.Uint32("uid", ent.New.IMAPUID),
			zap.Uint32("seqnum", ent.New.SeqNum),
			zap.Uint64("modseq", uint64(ent.New.ModSeq)),
		)
		if ent.New.IMAPUID > newMaxUID {
			newMaxUID = ent.New.IMAPUID
		}
		s.mbox.Msgs++
	}

	if hasNewMessages {
		log.Debug("sending new messages count", zap.Uint32("msgs_count", s.mbox.Msgs))
		if err := w.WriteNumMessages(s.mbox.Msgs); err != nil {
			return fmt.Errorf("failed to write num messages: %w", err)
		}
	}

	fetchFlagsID := make([]uint32, 0, len(entries))
	for i := len(entries) - 1; i >= 0; i-- {
		ent := entries[i]
		if ent.Updated == nil {
			continue
		}

		if newAt < ent.At {
			newAt = ent.At
		}
		if s.mbox.SkipFlagUpdateUntil[ent.Updated.IMAPUID] >=
			ent.At {
			log.Debug("skipped flag update",
				zap.Stringer("msg_id", ent.Updated.MsgID),
				zap.Uint64("modseq", uint64(ent.Updated.ModSeq)))
			continue
		}
		fetchFlagsID = append(fetchFlagsID, ent.Updated.IMAPUID)
	}
	if len(fetchFlagsID) != 0 {
		fetched, err := s.b.messages.Fetch(
			ctx, s.accountID, s.mbox.FolderID,
			folder.Range{
				Values:    fetchFlagsID,
				At:        s.mbox.At,
				DeletesAt: s.mbox.DeletesAt,
			}, 0,
			true)
		if err != nil {
			return fmt.Errorf("failed to fetch changes: %w", err)
		}
		for _, msg := range fetched {
			err := w.WriteMessageFlags(msg.Entry.SeqNum, imap.UID(msg.Entry.IMAPUID), stringListAsFlags(msg.Msg.Flags))
			if err != nil {
				return fmt.Errorf("failed to write flags: %w", err)
			}
		}
	}

	s.mbox.MaxUID = newMaxUID
	s.mbox.At = newAt
	s.mbox.SkipFlagUpdateUntil = map[uint32]folder.ModSeq{}

	return nil
}

func (s *session) updateRecents(ctx context.Context, w *imapserver.UpdateWriter) error {
	if s.enabledCaps.Has(imap.CapIMAP4rev2) {
		return nil
	}
	var (
		newRecents recent.Set
		err        error
	)

	log := contextlib.Logger(ctx)

	if s.mbox.ReadOnly {
		newRecents, err = s.b.recents.GetRecents(ctx, s.mbox.FolderID, s.mbox.At)
	} else {
		newRecents, err = s.b.recents.PopRecents(ctx, s.mbox.FolderID, s.mbox.At)
	}
	if err != nil {
		log.Error("failed to fetch new recents", zap.Error(err))
	} else if newRecents.Len() > 0 {
		log.Debug("added new recent entries", zap.Int("count", newRecents.Len()))
		s.mbox.Recents.MergeWith(&newRecents)
		if err := w.WriteNumRecent(uint32(s.mbox.Recents.Len())); err != nil {
			return err
		}
	}

	return nil
}

func (s *session) Poll(w *imapserver.UpdateWriter, allowExpunge bool) error {
	if !s.mbox.isOpen() {
		return nil
	}

	ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Poll")
	defer task.End()

	log := contextlib.Logger(ctx).WithLazy(
		zap.Stringer("imap_selected_id", s.mbox.FolderID),
		zap.Uint32("msgs_count", s.mbox.Msgs),
	)
	initialAt, initialDeletesAt := s.mbox.At, s.mbox.DeletesAt
	ctx = contextlib.WithLogger(ctx, log)

	changeMask := folder.ChangeNewMessage | folder.ChangeMessageUpdated
	if allowExpunge {
		changeMask |= folder.ChangeMessageDeleted
	}
	entries, err := s.b.watcher.Sync(
		ctx, []ulid.ULID{s.mbox.FolderID},
		s.mbox.At, s.mbox.DeletesAt, changeMask,
	)
	if err != nil {
		log.Error("watcher error, ignoring updates", zap.Error(err))
		return nil
	}

	if allowExpunge {
		if err := s.applyExpungeUpdates(ctx, w, entries); err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}

			log.Error("error in applyExpungeUpdates", zap.Error(err))
			return s.c.Bye("Poll failed, terminating connection to prevent corruption")
		}
	}

	if err := s.applyOtherUpdates(ctx, w, entries); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}

		log.Error("error in applyOtherUpdates", zap.Error(err))
		return s.c.Bye("Poll failed, terminating connection to prevent corruption")
	}

	if err := s.updateRecents(ctx, w); err != nil {
		log.Error("error in updateRecents", zap.Error(err))
		return s.c.Bye("Poll failed, terminating connection to prevent corruption")
	}

	// DeletesAt may lag behind if some Poll's are without expunges
	// but the reverse is not true - we always see updates/new messages.
	s.mbox.At = max(s.mbox.At, s.mbox.DeletesAt)
	log.Debug("synchronized",
		zap.Uint64("initial_modseq", uint64(initialAt)),
		zap.Uint64("initial_deletes_modseq", uint64(initialDeletesAt)),
		zap.Uint64("new_modseq", uint64(s.mbox.At)),
		zap.Uint64("new_deletes_modseq", uint64(s.mbox.DeletesAt)))

	return nil
}

func (s *session) Idle(w *imapserver.UpdateWriter, stop <-chan struct{}) error {
	ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Idle")
	defer task.End()

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	log := contextlib.Logger(ctx).WithLazy(
		zap.String("imap_command", "IDLE"),
		zap.Stringer("imap_selected_id", s.mbox.FolderID),
	)
	initialAt, initialDeletesAt := s.mbox.At, s.mbox.DeletesAt
	ctx = contextlib.WithLogger(ctx, log)

	go func() {
		<-stop
		cancel(errIdleStopped)
	}()

	for {
		entries, err := s.b.watcher.Wait(
			ctx, []ulid.ULID{s.mbox.FolderID},
			s.mbox.At, s.mbox.DeletesAt,
			folder.ChangeAllMessage,
		)
		if err != nil {
			log.Error("watcher error, ignoring updates", zap.Error(err))
			time.Sleep(5 * time.Second)
			continue
		}

		if err := s.applyExpungeUpdates(ctx, w, entries); err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}

			log.Error("error in applyExpungeUpdates", zap.Error(err))
			return s.c.Bye("IDLE failed, terminating connection to prevent corruption")
		}

		if err := s.applyOtherUpdates(ctx, w, entries); err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}

			log.Error("error in applyOtherUpdates", zap.Error(err))
			return s.c.Bye("IDLE failed, terminating connection to prevent corruption")
		}

		if err := s.updateRecents(ctx, nil); err != nil {
			log.Error("error in updateRecents", zap.Error(err))
			return s.c.Bye("IDLE failed, terminating connection to prevent corruption")
		}

		// DeletesAt may lag behind if some Poll's are without expunges
		// but the reverse is not true - we always see updates/new messages.
		s.mbox.At = max(s.mbox.At, s.mbox.DeletesAt)
		log.Debug("synchronized",
			zap.Uint64("initial_modseq", uint64(initialAt)),
			zap.Uint64("initial_deletes_modseq", uint64(initialDeletesAt)),
			zap.Uint64("new_modseq", uint64(s.mbox.At)),
			zap.Uint64("new_deletes_modseq", uint64(s.mbox.DeletesAt)))
	}
}
