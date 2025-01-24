package foldersqlite

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"runtime/trace"

	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/pkg/storeerrors"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite"
	"github.com/oklog/ulid/v2"
	"gorm.io/gorm"
)

type imapFolderDTO struct {
	FolderID    ulid.ULID `gorm:"column:folder_id"`
	UIDNext     uint32    `gorm:"column:uid_next"`
	UIDValidity uint32    `gorm:"column:uid_validity"`
}

func (imapFolderDTO) TableName() string { return "imap_folders" }

func (val imapFolderDTO) AsModel() folder.IMAPFolder {
	return folder.IMAPFolder{
		ID:          val.FolderID,
		UIDNext:     val.UIDNext,
		UIDValidity: val.UIDValidity,
	}
}

type IMAPRepo struct {
	db sqlite.DB
}

func New(db sqlite.DB) IMAPRepo {
	return IMAPRepo{db: db}
}

func (r IMAPRepo) CreateIMAPFolders(ctx context.Context, ids ...ulid.ULID) ([]folder.IMAPFolder, error) {
	dto := make([]imapFolderDTO, len(ids))
	for i, id := range ids {
		dto[i] = imapFolderDTO{
			FolderID:    id,
			UIDNext:     1,
			UIDValidity: rand.Uint32(),
		}
	}

	err := r.db.Gorm(ctx).Create(&dto).Error
	if err != nil {
		return nil, err
	}

	models := make([]folder.IMAPFolder, len(dto))
	for i, d := range dto {
		models[i] = d.AsModel()
	}

	return models, nil
}

func (r IMAPRepo) GetIMAPFolder(ctx context.Context, id ulid.ULID) (folder.IMAPFolder, error) {
	var dto imapFolderDTO
	err := r.db.Gorm(ctx).Where("folder_id = ?", id).First(&dto).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return folder.IMAPFolder{}, folder.ErrNotFound
		}
		return folder.IMAPFolder{}, storeerrors.InternalError{Reason: fmt.Errorf("failed to fetch imap folder info: %w", err)}
	}
	return dto.AsModel(), nil
}

func (r IMAPRepo) GetIMAPFolders(ctx context.Context, ids []ulid.ULID) ([]folder.IMAPFolder, error) {
	var dto []imapFolderDTO
	err := r.db.Gorm(ctx).Where("folder_id IN (?)", ids).Find(&dto).Error
	if err != nil {
		return nil, storeerrors.InternalError{Reason: fmt.Errorf("failed to fetch imap folder info: %w", err)}
	}

	models := make([]folder.IMAPFolder, len(dto))
	for i, d := range dto {
		models[i] = d.AsModel()
	}

	return models, nil
}

func (r IMAPRepo) NextUID(ctx context.Context, folderID ulid.ULID, n int) ([]uint32, error) {
	defer trace.StartRegion(ctx, "maddy-storage/folder.repository.sqlite.NextUID").End()

	if n <= 0 {
		panic("n must be positive")
	}

	var lastUID uint32

	err := r.db.Gorm(ctx).Raw(`
		UPDATE imap_folders 
		SET uid_next = uid_next + ?
		WHERE folder_id = ?
		RETURNING uid_next - 1`, n, folderID).Scan(&lastUID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, folder.ErrNotFound
		}
		return nil, storeerrors.InternalError{Reason: err}
	}

	uids := make([]uint32, 0, n)
	for i := lastUID - uint32(n) + 1; i <= lastUID; i++ {
		uids = append(uids, i)
	}

	return uids, nil
}

func (r IMAPRepo) LastModSeq(ctx context.Context, accountID ulid.ULID) (folder.ModSeq, error) {
	defer trace.StartRegion(ctx, "maddy-storage/folder.repository.sqlite.LastModSeq").End()

	var nextSeq uint64

	err := r.db.Gorm(ctx).Raw(`SELECT modseq FROM modseq WHERE account_id = ?`, accountID).Scan(&nextSeq).Error
	if err != nil {
		return 0, storeerrors.InternalError{Reason: err}
	}

	return folder.ModSeq(nextSeq), nil
}

func (r IMAPRepo) NextModSeq(ctx context.Context, accountID ulid.ULID) (folder.ModSeq, error) {
	defer trace.StartRegion(ctx, "maddy-storage/folder.repository.sqlite.NextModSeq").End()

	var nextSeq uint64

	err := r.db.Gorm(ctx).Raw(`
		INSERT INTO modseq
		VALUES (?, 1)
		ON CONFLICT (account_id)
		DO UPDATE SET modseq = modseq + 1
		RETURNING modseq`,
		accountID).Scan(&nextSeq).Error
	if err != nil {
		return 0, storeerrors.InternalError{Reason: err}
	}

	return folder.ModSeq(nextSeq), nil
}
