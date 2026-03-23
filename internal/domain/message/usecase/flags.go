package messageusecase

import (
	"context"
	"fmt"

	"github.com/foxcpp/maddy-storage/internal/domain/folder"
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

func (uc *Usecase) AddFlags(
	ctx context.Context, accountID, folderID ulid.ULID, numIDs folder.Range,
	flags []string, modSeqLe folder.ModSeq, returnSeq bool,
) ([]UpdatedMessage, error) {
	log := contextlib.FromContext(ctx)

	modSeq, err := uc.imapRepo.NextModSeq(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("next modseq: %w", err)
	}
	if modSeqLe == 0 {
		modSeqLe = modSeq
	}

	entries, err := uc.folderRepo.GetEntryByRange(ctx, folderID, numIDs, returnSeq)
	if err != nil {
		return nil, fmt.Errorf("get entry by range: %w", err)
	}

	entByID := make(map[ulid.ULID]*folder.Entry)
	ids := make([]ulid.ULID, 0, len(entries))
	for _, ent := range entries {
		if ent.ModSeq > modSeqLe {
			continue
		}
		ids = append(ids, ent.MsgID)
		entByID[ent.MsgID] = &ent
	}

	updatesMap, err := uc.msgRepo.AddFlags(ctx, ids, flags, modSeqLe, modSeq)
	if err != nil {
		return nil, fmt.Errorf("add flags: %w", err)
	}

	updates := make([]UpdatedMessage, 0, len(updatesMap))
	for msgID, flags := range updatesMap {
		log.Debug("set flags",
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

func (uc *Usecase) SetFlags(
	ctx context.Context, accountID, folderID ulid.ULID, numIDs folder.Range,
	flags []string, modSeqLe folder.ModSeq, returnSeq bool,
) ([]UpdatedMessage, error) {
	log := contextlib.FromContext(ctx)

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

	updatesMap, err := uc.msgRepo.ReplaceFlags(ctx, ids, flags, modSeqLe, modSeq)
	if err != nil {
		return nil, err
	}

	updates := make([]UpdatedMessage, 0, len(updatesMap))
	for msgID, flags := range updatesMap {
		log.Debug("set flags",
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

func (uc *Usecase) DeleteFlags(
	ctx context.Context, accountID, folderID ulid.ULID, numIDs folder.Range,
	flags []string, modSeqLe folder.ModSeq, returnSeq bool,
) ([]UpdatedMessage, error) {
	log := contextlib.FromContext(ctx)

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

	updatesMap, err := uc.msgRepo.DeleteFlags(ctx, ids, flags, modSeqLe, modSeq)
	if err != nil {
		return nil, err
	}

	updates := make([]UpdatedMessage, 0, len(updatesMap))
	for msgID, flags := range updatesMap {
		log.Debug("removed flags",
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
