package messageusecase

import (
	"context"
	"fmt"
	"time"

	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/domain/message"
	"github.com/foxcpp/maddy-storage/internal/domain/message/searcher"
	"github.com/foxcpp/maddy-storage/internal/pkg/contextlib"
	"github.com/oklog/ulid/v2"
	"go.uber.org/zap"
)

func (uc *Usecase) deleteNewPart(ctx context.Context, p *message.NewPart) error {
	if p.ExternalID != "" {
		return uc.blobStore.Delete(ctx, p.ExternalID)
	}
	return nil
}

type DeletedMsg struct {
	MsgID             ulid.ULID
	SeqNum            uint32
	UID               uint32
	PreviouslyDeleted bool
}

func (uc *Usecase) Delete(
	ctx context.Context, accountID, folderID ulid.ULID,
	flagged bool, ranges folder.Range, returnSeq bool,
) ([]DeletedMsg, error) {
	cond := searcher.Cond{
		FolderIDs: []ulid.ULID{folderID},
	}
	if !ranges.Empty() {
		cond.NumericIDs = []folder.Range{ranges}
	}
	if flagged {
		cond.Flag = []string{"\\Deleted"}
	}

	res, err := uc.searcher.Search(ctx, accountID, &cond, searcher.Opts{
		ReturnAll:     true,
		ReturnSeqNums: returnSeq,
		At:            ranges.At,
		DeletesAt:     ranges.DeletesAt,
	})
	if err != nil {
		return nil, err
	}

	deleted := make([]DeletedMsg, 0, len(res.All))
	delIDs := make([]ulid.ULID, 0, len(res.All))
	seqByUID := make(map[uint32]uint32, len(res.All))
	for _, m := range res.All {
		if !m.DeletedAt.IsZero() {
			deleted = append(deleted, DeletedMsg{
				MsgID:             m.MessageID,
				SeqNum:            m.SeqNum,
				UID:               m.UID,
				PreviouslyDeleted: true,
			})
			continue
		}
		delIDs = append(delIDs, m.MessageID)
		seqByUID[m.UID] = m.SeqNum
	}
	if len(delIDs) == 0 {
		return deleted, nil
	}

	delModSeq, err := uc.imapRepo.NextModSeq(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("modseq: %w", err)
	}

	deletedEnts, err := uc.folderRepo.DeleteEntryByIDs(ctx, folderID, delIDs, delModSeq)
	if err != nil {
		return nil, fmt.Errorf("failed to delete %d entries from folder %v: %w", len(delIDs), folderID, err)
	}
	for _, ent := range deletedEnts {
		deleted = append(deleted, DeletedMsg{
			MsgID:             ent.MsgID,
			SeqNum:            seqByUID[ent.IMAPUID], // its kinda expensive to calculate seqnums twice
			UID:               ent.IMAPUID,
			PreviouslyDeleted: false,
		})
	}
	contextlib.Logger(ctx).Info("messages soft-deleted",
		zap.Stringer("folder_id", folderID),
		zap.Stringer("account_id", accountID),
		zap.Uint64("modseq", uint64(delModSeq)),
		zap.Int("count", len(deletedEnts)),
	)

	return deleted, nil
}

func (uc *Usecase) deleteExternalIDs(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	ids, err := uc.msgRepo.DeletableExternalIDs(ctx, ids)
	if err != nil {
		return err
	}

	for _, id := range ids {
		if err := uc.blobStore.Delete(ctx, id); err != nil {
			return fmt.Errorf("delete blob %s: %w", id, err)
		}
	}

	return nil
}

func (uc *Usecase) CleanDanglingMessages(ctx context.Context) error {
	msgIDs, err := uc.msgRepo.DanglingMsgIDs(ctx, 1000)
	if err != nil {
		return fmt.Errorf("failed to fetch dangling messages for deletion: %w", err)
	}
	if len(msgIDs) == 0 {
		return nil
	}
	msgs, err := uc.msgRepo.GetByIDs(ctx, 0, msgIDs...)
	if err != nil {
		return fmt.Errorf("failed to fetch messages for deletion: %w", err)
	}
	if len(msgs) == 0 {
		return nil
	}

	removedSize := uint64(0)
	removedSizeExternal := uint64(0)

	externalIDs := make([]string, 0, len(msgs)*2)
	for _, msg := range msgs {
		for _, p := range msg.Parts {
			removedSize += uint64(p.TotalSize())
			if p.ExternalBlobID == "" {
				continue
			}
			removedSizeExternal += uint64(p.TotalSize())
			externalIDs = append(externalIDs, p.ExternalBlobID)
		}
	}

	if len(externalIDs) > 0 {
		if err := uc.deleteExternalIDs(ctx, externalIDs); err != nil {
			return err
		}
	}

	if err := uc.msgRepo.DeleteByID(ctx, msgIDs...); err != nil {
		return fmt.Errorf("failed to delete messages from db: %w", err)
	}

	contextlib.Logger(ctx).Info("dangling messages deleted from db and storage",
		zap.Int("count", len(msgIDs)),
		zap.Uint64("size", removedSize),
		zap.Uint64("external_size", removedSizeExternal),
	)

	return nil
}

func (uc *Usecase) CleanDeleted(ctx context.Context, accountID, folderID ulid.ULID) error {
	ents, err := uc.folderRepo.DeletedEntries(ctx, folderID, time.Now().Add(-time.Hour), 100)
	if err != nil {
		return fmt.Errorf("failed to fetch deleted entries for folder %v: %w", folderID, err)
	}
	if len(ents) == 0 {
		return nil
	}

	msgIDs := make([]ulid.ULID, 0, len(ents))
	for _, ent := range ents {
		msgIDs = append(msgIDs, ent.MsgID)
	}
	msgs, err := uc.msgRepo.GetByIDs(ctx, 0, msgIDs...)
	if err != nil {
		return fmt.Errorf("failed to fetch messages for deletion: %w", err)
	}
	if len(msgs) == 0 {
		return nil
	}

	removedSize := uint64(0)
	removedSizeExternal := uint64(0)

	externalIDs := make([]string, 0, len(msgs)*2)
	for _, msg := range msgs {
		for _, p := range msg.Parts {
			removedSize += uint64(p.TotalSize())
			if p.ExternalBlobID == "" {
				continue
			}
			removedSizeExternal += uint64(p.TotalSize())
			externalIDs = append(externalIDs, p.ExternalBlobID)
		}
	}

	if len(externalIDs) > 0 {
		if err := uc.deleteExternalIDs(ctx, externalIDs); err != nil {
			return err
		}
	}

	if err := uc.msgRepo.DeleteByID(ctx, msgIDs...); err != nil {
		return fmt.Errorf("failed to delete messages from db: %w", err)
	}

	contextlib.Logger(ctx).Info("messages deleted from db and storage",
		zap.Stringer("account_id", accountID),
		zap.Stringer("folder_id", folderID),
		zap.Int("count", len(msgIDs)),
		zap.Uint64("size", removedSize),
		zap.Uint64("external_size", removedSizeExternal),
	)

	return nil
}
