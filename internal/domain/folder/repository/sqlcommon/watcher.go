package foldersql

import (
	"context"
	"database/sql"
	"time"

	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlcommon"
	"github.com/oklog/ulid/v2"
)

type Watcher struct {
	pollInterval time.Duration
	db           sqlcommon.DB
}

func NewWatcher(pollInterval time.Duration, db sqlcommon.DB) *Watcher {
	return &Watcher{
		pollInterval: pollInterval,
		db:           db,
	}
}

type changedEntryDTO struct {
	// can't reuse entryDTO as it is ignored by gorm for some reason

	FolderID        ulid.ULID    `gorm:"column:folder_id"`
	MessageID       ulid.ULID    `gorm:"column:message_id"`
	UID             uint32       `gorm:"column:uid"`
	SeqNum          uint32       `gorm:"column:seq"`
	ModSeq          uint64       `gorm:"column:modseq"`
	CreatedAtModSeq uint64       `gorm:"column:created_at_modseq"`
	MsgModSeq       uint64       `gorm:"column:msg_modseq"`
	CreatedAt       time.Time    `gorm:"column:created_at"`
	DeletedAt       sql.NullTime `gorm:"column:deleted_at"`
}

func (w Watcher) Sync(ctx context.Context, folders []ulid.ULID, since, deletesSince folder.ModSeq, types folder.ChangeType) ([]folder.EntryChange, error) {
	if types == folder.ChangeNone {
		return nil, nil
	}

	var entries []changedEntryDTO
	q := w.db.Gorm(ctx).
		Table("folder_entries").
		Joins("JOIN messages ON messages.id = folder_entries.message_id").
		Joins(`JOIN (
			SELECT folder_entries.folder_id AS folder_id, 
                   folder_entries.uid AS uid,
			       row_number() OVER (PARTITION BY folder_id ORDER BY uid) AS seq
			FROM folder_entries
			JOIN folders ON folders.id = folder_entries.folder_id
			WHERE folder_entries.deleted_at IS NULL OR
				 (folder_entries.deleted_at IS NOT NULL AND folder_entries.modseq > ?)
		) seqnums 
		ON seqnums.uid = folder_entries.uid 
		AND seqnums.folder_id = folder_entries.folder_id`, deletesSince)
	// ^Different from joinSeqNums, we don't have an upper limit on seqnums here
	// so we can actually detect newer messages.

	conds := w.db.Gorm(ctx)
	if types.Includes(folder.ChangeMessageDeleted) {
		conds = conds.Or("folder_entries.modseq > ? AND folder_entries.deleted_at IS NOT NULL", deletesSince)
	}
	if types.Includes(folder.ChangeNewMessage) {
		conds = conds.Or("folder_entries.modseq > ? AND folder_entries.modseq = messages.modseq AND folder_entries.deleted_at IS NULL", since)
	}
	if types.Includes(folder.ChangeMessageUpdated) {
		conds = conds.Or("folder_entries.modseq != messages.modseq AND messages.modseq > ? AND  folder_entries.deleted_at IS NULL", since)
	}
	err := q.Select("folder_entries.*, messages.modseq AS msg_modseq, seqnums.seq").
		Order("folder_entries.folder_id, folder_entries.uid").
		Where("folder_entries.folder_id in (?)", folders).
		Where(conds).
		Limit(500).
		Scan(&entries).Error
	if err != nil {
		return nil, err
	}

	models := make([]folder.EntryChange, 0, len(entries))
	for _, dto := range entries {
		var deletedAt time.Time
		if dto.DeletedAt.Valid {
			deletedAt = dto.DeletedAt.Time
		}
		entry := folder.Entry{
			FolderID:        dto.FolderID,
			MsgID:           dto.MessageID,
			IMAPUID:         dto.UID,
			ModSeq:          folder.ModSeq(dto.ModSeq),
			CreatedAtModSeq: folder.ModSeq(dto.CreatedAtModSeq),
			SeqNum:          dto.SeqNum,
			CreatedAt:       dto.CreatedAt,
			DeletedAt:       deletedAt,
		}

		change := folder.EntryChange{}
		if !entry.DeletedAt.IsZero() {
			change.Deleted = &entry
			change.At = entry.ModSeq
		} else if dto.MsgModSeq == dto.ModSeq {
			change.New = &entry
		} else {
			change.Updated = &entry
		}
		change.At = folder.ModSeq(max(dto.ModSeq, dto.MsgModSeq))

		models = append(models, change)
	}
	return models, nil
}

func (w Watcher) Wait(ctx context.Context, folders []ulid.ULID, since, deletesSince folder.ModSeq, types folder.ChangeType) ([]folder.EntryChange, error) {
	return folder.Poll(ctx, w, w.pollInterval, folders, since, deletesSince, types)
}
