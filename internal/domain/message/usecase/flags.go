package messageusecase

import (
	"context"
	"fmt"

	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/domain/message"
	"github.com/foxcpp/maddy-storage/internal/pkg/contextlib"
	"github.com/oklog/ulid/v2"
	"go.uber.org/zap"
)

type UpdatedMessage struct {
	MsgID ulid.ULID
	UID   uint32
	Seq   uint32
	Flags []string
	At    folder.ModSeq

	PreviousAt         folder.ModSeq
	HadParallelChanges bool
}

func (uc *Usecase) AddFlagsByIDs(
	ctx context.Context, accountID ulid.ULID, ids []ulid.ULID,
	flags []string,
) error {
	modSeq, err := uc.imapRepo.NextModSeq(ctx, accountID)
	if err != nil {
		return fmt.Errorf("next modseq: %w", err)
	}

	_, err = uc.msgRepo.AddFlags(ctx, ids, flags, modSeq, modSeq)
	return err
}

func (uc *Usecase) SetFlagsByIDs(
	ctx context.Context, accountID ulid.ULID, ids []ulid.ULID,
	flags []string,
) error {
	modSeq, err := uc.imapRepo.NextModSeq(ctx, accountID)
	if err != nil {
		return fmt.Errorf("next modseq: %w", err)
	}

	_, err = uc.msgRepo.ReplaceFlags(ctx, ids, flags, modSeq, modSeq)
	return err
}

func (uc *Usecase) DeleteFlagsByIDs(
	ctx context.Context, accountID ulid.ULID, ids []ulid.ULID,
	flags []string,
) error {
	modSeq, err := uc.imapRepo.NextModSeq(ctx, accountID)
	if err != nil {
		return fmt.Errorf("next modseq: %w", err)
	}

	_, err = uc.msgRepo.DeleteFlags(ctx, ids, flags, modSeq, modSeq)
	return err
}

func (uc *Usecase) AddFlags(
	ctx context.Context, accountID, folderID ulid.ULID, numIDs folder.Range,
	flags []string, modSeqLe folder.ModSeq, returnSeq bool,
) ([]UpdatedMessage, error) {
	return uc.flagOp(
		ctx, accountID, folderID, numIDs,
		flags, modSeqLe, returnSeq,
		uc.msgRepo.AddFlags,
	)
}

func (uc *Usecase) SetFlags(
	ctx context.Context, accountID, folderID ulid.ULID, numIDs folder.Range,
	flags []string, modSeqLe folder.ModSeq, returnSeq bool,
) ([]UpdatedMessage, error) {
	return uc.flagOp(
		ctx, accountID, folderID, numIDs,
		flags, modSeqLe, returnSeq,
		uc.msgRepo.ReplaceFlags,
	)
}

func (uc *Usecase) DeleteFlags(
	ctx context.Context, accountID, folderID ulid.ULID, numIDs folder.Range,
	flags []string, modSeqLe folder.ModSeq, returnSeq bool,
) ([]UpdatedMessage, error) {
	return uc.flagOp(
		ctx, accountID, folderID, numIDs,
		flags, modSeqLe, returnSeq,
		uc.msgRepo.DeleteFlags,
	)
}

func (uc *Usecase) flagOp(
	ctx context.Context, accountID, folderID ulid.ULID, numIDs folder.Range,
	flags []string, modSeqLe folder.ModSeq, returnSeq bool,
	repoFunc func(ctx context.Context, ids []ulid.ULID, flags []string, modSeqLe folder.ModSeq, newModSeq folder.ModSeq) (map[ulid.ULID]message.MsgFlags, error),
) ([]UpdatedMessage, error) {
	log := contextlib.Logger(ctx)

	modSeq, err := uc.imapRepo.NextModSeq(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("next modseq: %w", err)
	}
	if modSeqLe == 0 {
		modSeqLe = modSeq
	}

	entries, err := uc.folderRepo.GetEntryByRange(ctx, folderID, numIDs, returnSeq)
	if err != nil {
		return nil, err
	}

	entByID := make(map[ulid.ULID]*folder.Entry)
	ids := make([]ulid.ULID, len(entries))
	for i, ent := range entries {
		ids[i] = ent.MsgID
		entByID[ent.MsgID] = &ent
	}

	updatesMap, err := repoFunc(ctx, ids, flags, modSeqLe, modSeq)
	if err != nil {
		return nil, err
	}

	updates := make([]UpdatedMessage, 0, len(updatesMap))
	for msgID, flags := range updatesMap {
		log.Debug("updated flags",
			zap.Stringer("msg_id", msgID),
			zap.Uint64("modseq", uint64(modSeq)),
			zap.Strings("flags", flags.Flags))
		updates = append(updates, UpdatedMessage{
			MsgID: msgID,
			UID:   entByID[msgID].IMAPUID,
			Seq:   entByID[msgID].SeqNum,
			Flags: flags.Flags,
			At:    flags.ModSeq,
		})
	}
	return updates, nil
}
