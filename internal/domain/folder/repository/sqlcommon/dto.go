package foldersql

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/oklog/ulid/v2"
)

type folderDTO struct {
	ID        ulid.ULID  `gorm:"column:id"`
	ParentID  *ulid.ULID `gorm:"column:parent_id;type:blob"` // actually []byte, see https://github.com/glebarez/go-sqlite/issues/181
	AccountID ulid.ULID  `gorm:"column:account_id"`

	Name string `gorm:"name"`
	Path string `gorm:"path"`

	Role       sql.NullString `gorm:"column:role"`
	Subscribed int            `gorm:"column:subscribed"`
	SortOrder  uint           `gorm:"column:sort_order"`

	Meta      json.RawMessage `gorm:"column:meta"`
	CreatedAt time.Time       `gorm:"column:created_at;autoCreateTime:false"`
	UpdatedAt time.Time       `gorm:"column:updated_at;autoUpdateTime:false"`
}

func (folderDTO) TableName() string { return "folders" }

func asDTO(model *folder.Folder) *folderDTO {
	subscribed := 0
	if model.Subscribed {
		subscribed = 1
	}
	role := sql.NullString{}
	if model.Role != "" {
		role.Valid = true
		role.String = string(model.Role)
	}
	metaJSON, err := json.Marshal(model.Metadata_)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal metadata for folder %v: %v", model.ID, err))
	}

	// XXX: For some unknown reason modernc.org SQLite returns FOREIGN KEY violations
	// when []byte(nil) is used so stuff it into a string with sql.NullString.
	var parentID *ulid.ULID
	if model.ParentID != (ulid.ULID{}) {
		parentID = &model.ParentID
	}

	return &folderDTO{
		ID:         model.ID,
		ParentID:   parentID,
		AccountID:  model.AccountID,
		Name:       model.Name,
		Path:       model.Path,
		Role:       role,
		Subscribed: subscribed,
		SortOrder:  model.SortOrder,
		Meta:       metaJSON,
		CreatedAt:  model.CreatedAt,
		UpdatedAt:  model.UpdatedAt,
	}
}

func asModel(dto *folderDTO) *folder.Folder {
	model := &folder.Folder{
		ID:               dto.ID,
		AccountID:        dto.AccountID,
		Name:             dto.Name,
		Path:             dto.Path,
		Subscribed:       dto.Subscribed != 0,
		SortOrder:        dto.SortOrder,
		CreatedAt:        dto.CreatedAt,
		UpdatedAt:        dto.UpdatedAt,
		InitialUpdatedAt: dto.UpdatedAt,
	}

	if dto.Role.Valid {
		model.Role = folder.Role(dto.Role.String)
	}

	if err := json.Unmarshal(dto.Meta, &model.Metadata_); err != nil {
		panic(fmt.Sprintf("failed to unmarshal metadata for folder %v: %v", model.ID, err))
	}

	if dto.ParentID != nil {
		model.ParentID = *dto.ParentID
	}

	return model
}

type entryDTO struct {
	FolderID        ulid.ULID    `gorm:"column:folder_id"`
	MessageID       ulid.ULID    `gorm:"column:message_id"`
	UID             uint32       `gorm:"column:uid"`
	ModSeq          uint64       `gorm:"column:modseq"`
	CreatedAtModSeq uint64       `gorm:"column:created_at_modseq"`
	Seq             uint32       `gorm:"column:seq;<-:false"`
	CreatedAt       time.Time    `gorm:"column:created_at"`
	DeletedAt       sql.NullTime `gorm:"column:deleted_at"` // Never read or used outside of repo and searcher.
}

func (entryDTO) TableName() string { return "folder_entries" }

func entryAsDTO(entry *folder.Entry) *entryDTO {
	return &entryDTO{
		FolderID:        entry.FolderID,
		MessageID:       entry.MsgID,
		UID:             entry.IMAPUID,
		CreatedAtModSeq: uint64(entry.CreatedAtModSeq),
		ModSeq:          uint64(entry.ModSeq),
		CreatedAt:       entry.CreatedAt,
	}
}

func entryAsModel(dto *entryDTO) *folder.Entry {
	var deletedAt time.Time
	if dto.DeletedAt.Valid {
		deletedAt = dto.DeletedAt.Time
	}

	return &folder.Entry{
		FolderID:        dto.FolderID,
		MsgID:           dto.MessageID,
		IMAPUID:         dto.UID,
		CreatedAtModSeq: folder.ModSeq(dto.CreatedAtModSeq),
		ModSeq:          folder.ModSeq(dto.ModSeq),
		SeqNum:          dto.Seq,
		CreatedAt:       dto.CreatedAt,
		DeletedAt:       deletedAt,
	}
}
