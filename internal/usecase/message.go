package usecase

import (
	"context"

	"github.com/foxcpp/maddy-storage/internal/domain/changelog"
	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/domain/message"
	"github.com/foxcpp/maddy-storage/internal/pkg/contextlog"
	"github.com/oklog/ulid/v2"
	"go.uber.org/zap"
)

type Message struct {
	folderRepo folder.Repo
	msgRepo    message.Repo
	changeLog  changelog.Repo
}

func NewMessage(folder folder.Repo, msg message.Repo, changeLog changelog.Repo) Message {
	return Message{
		folderRepo: folder,
		msgRepo:    msg,
		changeLog:  changeLog,
	}
}

type CopyData struct {
	Source        *folder.Folder
	Target        *folder.Folder
	SourceEntries []folder.Entry
	TargetEntries []folder.Entry
}

func (m Message) CopyByUID(ctx context.Context, accountID ulid.ULID, uids []folder.UIDRange, sourceID ulid.ULID, targetPath string) (*CopyData, error) {
	log := contextlog.FromContext(ctx)

	sourceFolder, err := m.folderRepo.GetByID(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	if sourceFolder.AccountID_ != accountID {
		return nil, folder.ErrNotFound
	}
	targetFolder, err := m.folderRepo.GetByPath(ctx, accountID, targetPath)
	if err != nil {
		return nil, err
	}

	log = log.With(zap.Stringer("source_folder_id", sourceFolder.ID_), zap.Stringer("target_folder_id", targetFolder.ID_))

	copyData := &CopyData{
		Source: sourceFolder,
		Target: targetFolder,
	}

	sourceEntries, err := m.folderRepo.GetEntryByUIDRange(ctx, sourceFolder.ID_, uids...)
	if err != nil {
		return nil, err
	}
	msgIDs := make([]ulid.ULID, 0, len(sourceEntries))
	for _, sourceEntry := range sourceEntries {
		msgIDs = append(msgIDs, sourceEntry.MsgID_)
	}

	msgs, err := m.msgRepo.GetByIDs(ctx, msgIDs...)
	if err != nil {
		return nil, err
	}
	// CONSISTENCY: Some messages might be gone at this point, will skip later.
	oldToNewMsgID := make(map[ulid.ULID]ulid.ULID, len(msgs))
	for i, msg := range msgs {
		oldID := msg.ID_
		msgs[i] = *msg.Copy()
		oldToNewMsgID[oldID] = msgs[i].ID_
	}

	log.Debug("resolved uid range to entries", zap.Stringers("entries", sourceEntries))

	targetUIDs, err := m.folderRepo.NextUID(ctx, targetFolder.ID_, len(sourceEntries))
	if err != nil {
		// CONSISTENCY: Folder might be gone, will return folder.ErrNotFound
		return nil, err
	}
	targetEntries := make([]folder.Entry, 0, len(msgs))
	sourceToTargetEnt := make(map[ulid.ULID]*folder.Entry, len(msgs))
	for i, e := range sourceEntries {
		// CONSISTENCY: Some created entries might referer to non-existing messages now.
		newMsgID, ok := oldToNewMsgID[e.MsgID_]
		if !ok {
			log.Info("message disappeared while copy is in progress", zap.Stringer("msg_id", e.MsgID_))
			continue
		}
		targetEntries = append(targetEntries,
			folder.NewEntry(targetFolder.ID_, newMsgID, targetUIDs[i]))
		sourceToTargetEnt[e.MsgID_] = &targetEntries[i]
	}

	log.Debug("created target entries", zap.Stringers("entries", targetEntries))

	// CONSISTENCY: Might create dangling messages if next operation fails, will be GC'ed later.
	// TODO: Might create messages that include missing external parts. Need to figure out
	// a way to defend against it.
	if err := m.msgRepo.Create(ctx, msgs...); err != nil {
		return nil, err
	}

	if err := m.folderRepo.CreateEntry(ctx, targetEntries...); err != nil {
		return nil, err
	}

	copyData.SourceEntries = sourceEntries
	copyData.TargetEntries = targetEntries

	if len(copyData.TargetEntries) < len(copyData.SourceEntries) {
		// Some messages disappeared during copy, need to filter.
		copyData.SourceEntries = copyData.SourceEntries[:0]
		copyData.TargetEntries = make([]folder.Entry, 0, len(copyData.TargetEntries))
		for _, entry := range sourceEntries {
			targetEnt, ok := sourceToTargetEnt[entry.MsgID_]
			if !ok {
				continue
			}
			copyData.TargetEntries = append(copyData.TargetEntries, *targetEnt)
		}
	}

	log.Info("copied messages", zap.Int("count", len(copyData.TargetEntries)))

	return copyData, nil
}

func (m Message) MoveByUID(ctx context.Context, accountID ulid.ULID, uids []folder.UIDRange, sourceID ulid.ULID, targetPath string) (*CopyData, error) {
	log := contextlog.FromContext(ctx)

	sourceFolder, err := m.folderRepo.GetByID(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	if sourceFolder.AccountID_ != accountID {
		return nil, folder.ErrNotFound
	}
	targetFolder, err := m.folderRepo.GetByPath(ctx, accountID, targetPath)
	if err != nil {
		return nil, err
	}

	log = log.With(zap.Stringer("source_folder_id", sourceFolder.ID_), zap.Stringer("target_folder_id", targetFolder.ID_))

	copyData := &CopyData{
		Source: sourceFolder,
		Target: targetFolder,
	}

	sourceEntries, err := m.folderRepo.GetEntryByUIDRange(ctx, sourceFolder.ID_, uids...)
	if err != nil {
		return nil, err
	}

	log.Debug("resolved uid range to entries", zap.Stringers("entries", sourceEntries))

	targetUIDs, err := m.folderRepo.NextUID(ctx, targetFolder.ID_, len(sourceEntries))
	if err != nil {
		// CONSISTENCY: Folder might be gone, will return folder.ErrNotFound
		return nil, err
	}
	targetEntries := make([]folder.Entry, 0, len(targetUIDs))
	for i, e := range sourceEntries {
		targetEntries = append(targetEntries,
			folder.NewEntry(targetFolder.ID_, e.MsgID_, targetUIDs[i]))
	}

	log.Debug("created target entries for move", zap.Stringers("entries", targetEntries))

	if err := m.folderRepo.ReplaceEntries(ctx, sourceEntries, targetEntries); err != nil {
		return nil, err
	}

	copyData.SourceEntries = sourceEntries
	copyData.TargetEntries = targetEntries

	log.Info("moved messages", zap.Int("count", len(copyData.TargetEntries)))

	return copyData, nil
}
