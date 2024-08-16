package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/foxcpp/maddy-storage/internal/domain/changelog"
	"github.com/foxcpp/maddy-storage/internal/pkg/storeerrors"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite"
	"github.com/oklog/ulid/v2"
)

type repo struct {
	db sqlite.DB
}

func New(db sqlite.DB) changelog.Repo {
	return repo{db: db}
}

func (r repo) LastAccountTime(ctx context.Context, accountID ulid.ULID) (time.Time, error) {
	var d struct{ At sql.NullTime }

	err := r.db.Gorm(ctx).
		Model(&entryDTO{}).
		Select("max(at) AS at").
		Where("changelog_entries.account_id = ?", accountID).
		Find(&d).Error

	if err != nil {
		return time.Time{}, storeerrors.InternalError{Reason: err}
	}
	if !d.At.Valid {
		d.At.Time = time.Time{}
	}

	return d.At.Time, nil
}

func (r repo) LastFolderTime(ctx context.Context, folderID ulid.ULID) (time.Time, error) {
	var d struct{ At sql.NullTime }

	err := r.db.Gorm(ctx).
		Model(&entryDTO{}).
		Select("max(at) AS at").
		Where("changelog_entries.folder_id = ?", folderID).
		Find(&d).Error

	if err != nil {
		return time.Time{}, storeerrors.InternalError{Reason: err}
	}
	if !d.At.Valid {
		d.At.Time = time.Time{}
	}

	return d.At.Time, nil
}

func (r repo) LastMessageTime(ctx context.Context, messageID ulid.ULID) (time.Time, error) {
	var d struct{ At sql.NullTime }

	err := r.db.Gorm(ctx).
		Model(&entryDTO{}).
		Select("max(at) AS at").
		Where("changelog_entries.message_id = ?", messageID).
		Find(&d).Error

	if err != nil {
		return time.Time{}, storeerrors.InternalError{Reason: err}
	}
	if !d.At.Valid {
		d.At.Time = time.Time{}
	}

	return d.At.Time, nil
}

func (r repo) GetAccountChanges(ctx context.Context, accountID ulid.ULID, atGt time.Time, limit int) ([]changelog.Entry, error) {
	var dtos []entryDTO

	q := r.db.Gorm(ctx).
		Model(&entryDTO{}).
		Where("changelog_entries.account_id = ?", accountID)
	if !atGt.IsZero() {
		q = q.Where("changelog_entries.at > ?", atGt)
	}
	if limit != 0 {
		q = q.Limit(limit)
	}
	if err := q.Find(&dtos).Error; err != nil {
		return nil, err
	}

	entries := make([]changelog.Entry, len(dtos))
	for i, dto := range dtos {
		entries[i] = *asModel(&dto)
	}
	return entries, nil
}

func (r repo) GetFolderChanges(ctx context.Context, folderID ulid.ULID, atGt time.Time, limit int) ([]changelog.Entry, error) {
	var dtos []entryDTO

	q := r.db.Gorm(ctx).
		Model(&entryDTO{}).
		Where("changelog_entries.folder_id = ?", folderID)
	if !atGt.IsZero() {
		q = q.Where("changelog_entries.at > ?", atGt)
	}
	if limit != 0 {
		q = q.Limit(limit)
	}
	if err := q.Find(&dtos).Error; err != nil {
		return nil, err
	}

	entries := make([]changelog.Entry, len(dtos))
	for i, dto := range dtos {
		entries[i] = *asModel(&dto)
	}
	return entries, nil
}

func (r repo) GetMessageChanges(ctx context.Context, msgID ulid.ULID, atGt time.Time, limit int) ([]changelog.Entry, error) {
	var dtos []entryDTO

	q := r.db.Gorm(ctx).
		Model(&entryDTO{}).
		Where("changelog_entries.message_id = ?", msgID)
	if !atGt.IsZero() {
		q = q.Where("changelog_entries.at > ?", atGt)
	}
	if limit != 0 {
		q = q.Limit(limit)
	}
	if err := q.Find(&dtos).Error; err != nil {
		return nil, err
	}

	entries := make([]changelog.Entry, len(dtos))
	for i, dto := range dtos {
		entries[i] = *asModel(&dto)
	}
	return entries, nil
}

func (r repo) Create(ctx context.Context, entries ...changelog.Entry) error {
	dtos := make([]entryDTO, len(entries))
	for i, ent := range entries {
		dtos[i] = *asDTO(&ent)
	}

	return r.db.Gorm(ctx).Create(dtos).Error
}
