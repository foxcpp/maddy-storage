package messageusecase

import (
	"context"
	"fmt"
	"runtime/trace"
	"time"

	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/pkg/contextlib"
	"github.com/oklog/ulid/v2"
	"go.uber.org/zap"
)

type CopyData struct {
	Source        *folder.Folder
	Target        *folder.Folder
	TargetIMAP    *folder.IMAPFolder
	SourceEntries []folder.Entry
	TargetEntries []folder.Entry
	ModSeq        folder.ModSeq
}

func (uc *Usecase) Copy(ctx context.Context, accountID ulid.ULID, ids folder.Range, sourceID ulid.ULID, targetPath string) (*CopyData, error) {
	defer trace.StartRegion(ctx, "maddy-storage/message.usecase.Copy").End()

	log := contextlib.Logger(ctx)

	sourceFolder, err := uc.folderRepo.GetByID(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	if sourceFolder.AccountID != accountID {
		return nil, folder.ErrNotFound
	}
	targetFolder, err := uc.folderRepo.GetByPath(ctx, accountID, targetPath)
	if err != nil {
		return nil, err
	}
	targetIMAP, err := uc.imapRepo.GetIMAPFolder(ctx, targetFolder.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to find IMAP folder: %w", err)
	}

	log = log.With(
		zap.Stringer("source_folder_id", sourceFolder.ID),
		zap.Stringer("target_folder_id", targetFolder.ID),
	)

	copyData := &CopyData{
		Source:     sourceFolder,
		Target:     targetFolder,
		TargetIMAP: &targetIMAP,
	}

	copyModSeq, err := uc.imapRepo.NextModSeq(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("modseq: %w", err)
	}
	copyData.ModSeq = copyModSeq

	sourceEntries, err := uc.folderRepo.GetEntryByRange(ctx, sourceFolder.ID, ids, false)
	if err != nil {
		return nil, err
	}
	msgIDs := make([]ulid.ULID, 0, len(sourceEntries))
	for _, sourceEntry := range sourceEntries {
		msgIDs = append(msgIDs, sourceEntry.MsgID)
	}

	msgs, err := uc.msgRepo.GetByIDs(ctx, 0, msgIDs...)
	if err != nil {
		return nil, err
	}
	// CONSISTENCY: Some messages might be gone at this point, will skip later.
	oldToNewMsgID := make(map[ulid.ULID]ulid.ULID, len(msgs))
	for i, msg := range msgs {
		oldID := msg.ID
		msgs[i] = *msg.Copy(copyModSeq)
		oldToNewMsgID[oldID] = msgs[i].ID
	}

	log.Debug("resolved uid range to entries", zap.Stringers("entries", sourceEntries))

	targetUIDs, err := uc.imapRepo.NextUID(ctx, targetFolder.ID, len(sourceEntries))
	if err != nil {
		// CONSISTENCY: Folder might be gone, will return folder.ErrNotFound
		return nil, fmt.Errorf("nextuid: %w", err)
	}
	targetEntries := make([]folder.Entry, 0, len(msgs))
	sourceToTargetEnt := make(map[ulid.ULID]*folder.Entry, len(msgs))
	for i, e := range sourceEntries {
		// CONSISTENCY: Some created entries might referer to non-existing messages now.
		newMsgID, ok := oldToNewMsgID[e.MsgID]
		if !ok {
			log.Info("message disappeared while copy is in progress", zap.Stringer("msg_id", e.MsgID))
			continue
		}
		targetEntries = append(targetEntries,
			folder.NewEntry(targetFolder.ID, newMsgID, targetUIDs[i], copyModSeq, time.Now()))
		sourceToTargetEnt[e.MsgID] = &targetEntries[i]
	}

	log.Debug("created target entries", zap.Stringers("entries", targetEntries))

	// CONSISTENCY: Might create dangling messages if next operation fails, will be GC'ed later.
	// TODO: Might create messages that include missing external parts. Need to figure out
	// a way to defend against it.
	if err := uc.msgRepo.Create(ctx, msgs...); err != nil {
		return nil, err
	}

	if err := uc.folderRepo.CreateEntry(ctx, targetEntries...); err != nil {
		return nil, err
	}

	copyData.SourceEntries = sourceEntries
	copyData.TargetEntries = targetEntries

	if len(copyData.TargetEntries) < len(copyData.SourceEntries) {
		// Some messages disappeared during copy, need to filter.
		copyData.SourceEntries = copyData.SourceEntries[:0]
		copyData.TargetEntries = make([]folder.Entry, 0, len(copyData.TargetEntries))
		for _, entry := range sourceEntries {
			targetEnt, ok := sourceToTargetEnt[entry.MsgID]
			if !ok {
				continue
			}
			copyData.TargetEntries = append(copyData.TargetEntries, *targetEnt)
		}
	}

	log.Info("copied messages",
		zap.Int("count", len(copyData.TargetEntries)),
		zap.Uint64("modseq", uint64(copyModSeq)))

	return copyData, nil
}

func (uc *Usecase) Move(ctx context.Context, accountID ulid.ULID,
	ids folder.Range, sourceID ulid.ULID, targetPath string,
	returnSeq bool,
) (*CopyData, error) {
	defer trace.StartRegion(ctx, "maddy-storage/message.usecase.Move").End()

	log := contextlib.Logger(ctx)

	sourceFolder, err := uc.folderRepo.GetByID(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	if sourceFolder.AccountID != accountID {
		return nil, folder.ErrNotFound
	}
	targetFolder, err := uc.folderRepo.GetByPath(ctx, accountID, targetPath)
	if err != nil {
		return nil, err
	}
	targetIMAP, err := uc.imapRepo.GetIMAPFolder(ctx, targetFolder.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to find IMAP folder: %w", err)
	}

	log = log.With(
		zap.Stringer("source_folder_id", sourceFolder.ID),
		zap.Stringer("target_folder_id", targetFolder.ID),
	)

	copyData := &CopyData{
		Source:     sourceFolder,
		Target:     targetFolder,
		TargetIMAP: &targetIMAP,
	}
	moveModSeq, err := uc.imapRepo.NextModSeq(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("mext modseq: %w", err)
	}
	copyData.ModSeq = moveModSeq

	sourceEntries, err := uc.folderRepo.GetEntryByRange(ctx, sourceFolder.ID, ids, returnSeq)
	if err != nil {
		return nil, err
	}

	log.Debug("resolved uid range to entries", zap.Stringers("entries", sourceEntries))

	targetUIDs, err := uc.imapRepo.NextUID(ctx, targetFolder.ID, len(sourceEntries))
	if err != nil {
		// CONSISTENCY: Folder might be gone, will return folder.ErrNotFound
		return nil, err
	}
	targetEntries := make([]folder.Entry, 0, len(targetUIDs))
	sourceIDs := make([]ulid.ULID, len(sourceEntries))
	for i, e := range sourceEntries {
		sourceIDs[i] = e.MsgID
		targetEntries = append(targetEntries,
			folder.NewEntry(targetFolder.ID, e.MsgID, targetUIDs[i], moveModSeq, time.Now()))
	}

	if err := uc.msgRepo.TouchMsgs(ctx, sourceIDs, moveModSeq); err != nil {
		return nil, fmt.Errorf("touch msgs: %w", err)
	}

	log.Debug("created target entries for move", zap.Stringers("entries", targetEntries))

	if err := uc.folderRepo.ReplaceEntries(ctx, sourceEntries, targetEntries, moveModSeq); err != nil {
		return nil, err
	}

	copyData.SourceEntries = sourceEntries
	copyData.TargetEntries = targetEntries

	log.Info("moved messages",
		zap.Int("count", len(copyData.TargetEntries)),
		zap.Uint64("modseq", uint64(moveModSeq)))

	return copyData, nil
}
