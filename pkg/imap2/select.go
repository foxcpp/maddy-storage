package imap2

import (
	"context"
	"runtime/trace"

	"github.com/emersion/go-imap/v2"
	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/domain/folder/recent"
	messageusecase "github.com/foxcpp/maddy-storage/internal/domain/message/usecase"
	"github.com/foxcpp/maddy-storage/internal/pkg/contextlib"
	"go.uber.org/zap"
)

var predefinedFlags = []imap.Flag{
	imap.FlagDeleted, imap.FlagFlagged, imap.FlagAnswered,
	imap.FlagDraft, imap.FlagSeen,
}

func (s *session) Select(mailbox string, options *imap.SelectOptions) (*imap.SelectData, error) {
	ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Select")
	defer task.End()

	log := s.log.WithLazy(
		zap.String("imap_command", "SELECT"),
		zap.String("imap_mailbox", mailbox),
		zap.Any("imap_opts", options))
	ctx = contextlib.WithLogger(ctx, log)

	s.enabledCaps = s.c.EnabledCaps()

	if s.mbox.isOpen() {
		if err := s.unselect(ctx); err != nil {
			log.Error("unselect failed", zap.Error(err))
			return nil, s.c.Bye("Unselect failed, connection is in unknown state")
		}
	}

	info, err := s.b.messages.FetchFolderInfo(ctx, s.accountID, mailbox,
		messageusecase.InfoOpts{
			CountMsgs:      true,
			ReturnIMAPMeta: true,
			ReturnMaxUID:   true,
			ReturnModSeq:   true,
			UsedFlags:      true,
		})
	if err != nil {
		return nil, s.asIMAPError(err)
	}
	usedFlagsMap := make(map[string]bool, len(info.UsedFlags))
	for _, val := range info.UsedFlags {
		usedFlagsMap[val] = true
	}
	for _, flag := range predefinedFlags {
		if !usedFlagsMap[string(flag)] {
			info.UsedFlags = append(info.UsedFlags, string(flag))
		}
	}

	var recents recent.Set
	if !s.enabledCaps.Has(imap.CapIMAP4rev2) {
		if options.ReadOnly {
			recents, err = s.b.recents.GetRecents(ctx, info.Folder.ID, info.AcctModSeq)
		} else {
			recents, err = s.b.recents.PopRecents(ctx, info.Folder.ID, info.AcctModSeq)
		}
		if err != nil {
			log.Error("failed to read recents, no flags will be set for this session", zap.Error(err))
		}
	}

	s.mbox = selectedMbox{
		FolderID:          info.Folder.ID,
		At:                info.AcctModSeq,
		DeletesAt:         info.AcctModSeq,
		Msgs:              info.Msgs,
		MaxUID:            info.MaxUID,
		Recents:           recents,
		ReadOnly:          options.ReadOnly,
		CondStoreActive:   options.CondStore,
		SavedSearchResult: imap.UIDSetNum(),

		SkipExpunges:        map[uint32]struct{}{},
		SkipFlagUpdateUntil: map[uint32]folder.ModSeq{},
	}

	log.Info(
		"folder open",
		zap.Stringer("folder_id", s.mbox.FolderID),
		zap.Uint64("modseq", uint64(info.AcctModSeq)),
	)

	// TODO: Use read-only flag for optimizations.

	return &imap.SelectData{
		Flags:          stringListAsFlags(info.UsedFlags),
		PermanentFlags: stringListAsFlags(append(info.UsedFlags, `\*`)),
		NumMessages:    info.Msgs,
		NumRecent:      uint32(recents.Len()),
		UIDNext:        imap.UID(info.IMAP.UIDNext),
		UIDValidity:    info.IMAP.UIDValidity,
		List: &imap.ListData{
			Attrs:   nil,
			Delim:   rune(folder.PathSeparator[0]),
			Mailbox: info.Folder.Path,
		},
	}, nil
}

func (s *session) Unselect() error {
	ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Unselect")
	defer task.End()

	return s.unselect(ctx)
}

func (s *session) unselect(ctx context.Context) error {
	s.mbox = selectedMbox{}
	return nil
}
