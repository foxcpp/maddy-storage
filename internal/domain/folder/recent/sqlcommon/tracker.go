package recentsql

import (
	"context"
	"fmt"

	"github.com/emersion/go-imap/v2"
	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/domain/folder/recent"
	"github.com/foxcpp/maddy-storage/internal/pkg/storeerrors"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlcommon"
	"github.com/oklog/ulid/v2"
	"gorm.io/gorm/clause"
)

type Tracker struct {
	// TODO: Limit recents set size.
	db sqlcommon.DB
}

func New(db sqlcommon.DB) Tracker {
	return Tracker{db: db}
}

type recentDTO struct {
	FolderID ulid.ULID `gorm:"column:folder_id"`
	UID      uint32    `gorm:"column:uid"`
	ModSeq   uint64    `gorm:"column:modseq"`
}

func (recentDTO) TableName() string { return "recent_uids" }

func (t Tracker) PopRecents(ctx context.Context, folderID ulid.ULID, modSeqLe folder.ModSeq) (recent.Set, error) {
	var recents []recentDTO
	err := t.db.Gorm(ctx).Table("recent_uids").Clauses(
		clause.Returning{
			Columns: []clause.Column{{Name: "uid"}},
		}).
		Where("recent_uids.folder_id = ?", folderID).
		Where("recent_uids.modseq <= ?", modSeqLe).
		Delete(&recents).Error
	if err != nil {
		return recent.Set{}, storeerrors.InternalError{Reason: fmt.Errorf("failed to fetch recent flag state: %w", err)}
	}
	set := imap.UIDSet{}
	for _, r := range recents {
		set.AddNum(imap.UID(r.UID))
	}
	return recent.Set{Set: set, Size: len(recents)}, nil
}

func (t Tracker) GetRecents(ctx context.Context, folderID ulid.ULID, modSeqLe folder.ModSeq) (recent.Set, error) {
	var recents []recentDTO
	err := t.db.Gorm(ctx).Table("recent_uids").
		Where("recent_uids.folder_id = ?", folderID).
		Where("recent_uids.modseq <= ?", modSeqLe).
		Find(&recents).Error
	if err != nil {
		return recent.Set{}, storeerrors.InternalError{Reason: fmt.Errorf("failed to fetch recent flag state: %w", err)}
	}
	set := imap.UIDSet{}
	for _, r := range recents {
		set.AddNum(imap.UID(r.UID))
	}
	return recent.Set{Set: set, Size: len(recents)}, nil
}

func (t Tracker) AddRecent(ctx context.Context, folderID ulid.ULID, uid imap.UID, modSeq folder.ModSeq) error {
	err := t.db.Gorm(ctx).Table("recent_uids").Clauses(clause.OnConflict{
		DoNothing: true,
	}).Create(&recentDTO{
		FolderID: folderID,
		UID:      uint32(uid),
		ModSeq:   uint64(modSeq),
	}).Error
	if err != nil {
		return storeerrors.InternalError{Reason: fmt.Errorf("failed to save recent flag state: %w", err)}
	}
	return nil
}

func (t Tracker) AddRecentEntries(ctx context.Context, ents []folder.Entry) error {
	dtos := make([]recentDTO, len(ents))
	for i, ent := range ents {
		dtos[i] = recentDTO{
			FolderID: ent.FolderID,
			UID:      ent.IMAPUID,
			ModSeq:   uint64(ent.ModSeq),
		}
	}
	err := t.db.Gorm(ctx).Table("recent_uids").Clauses(clause.OnConflict{
		DoNothing: true,
	}).Create(dtos).Error
	if err != nil {
		return storeerrors.InternalError{Reason: fmt.Errorf("failed to save recent flag state: %w", err)}
	}
	return nil
}

func (t Tracker) CountRecent(ctx context.Context, folderID ulid.ULID) (uint32, error) {
	var cnt int64
	err := t.db.Gorm(ctx).Table("recent_uids").
		Where("recent_uids.folder_id = ?", folderID).
		Count(&cnt).Error
	if err != nil {
		return 0, storeerrors.InternalError{Reason: fmt.Errorf("failed to fetch recent flag state: %w", err)}
	}

	return uint32(cnt), nil
}
