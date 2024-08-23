package sqlcommon

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/oklog/ulid/v2"
	"time"
)

type FolderDTO struct {
	ID        ulid.ULID `gorm:"id"`
	ParentID  []byte    `gorm:"parent_id"`
	AccountID ulid.ULID `gorm:"account_id"`

	Name string `gorm:"name"`
	Path string `gorm:"path"`

	Role       sql.NullString `gorm:"role"`
	Subscribed int            `gorm:"subscribed"`
	SortOrder  uint           `gorm:"sort_order"`

	UIDNext     uint32 `json:"uid_next"`
	UIDValidity uint32 `gorm:"uid_validity"`

	Meta      json.RawMessage `gorm:"meta"`
	CreatedAt time.Time       `gorm:"created_at,autoCreateTime:false"`
	UpdatedAt time.Time       `gorm:"updated_at,autoUpdateTime:false"`
}

func (FolderDTO) TableName() string { return "folders" }

func AsDTO(model *folder.Folder) *FolderDTO {
	subscribed := 0
	if model.Subscribed_ {
		subscribed = 1
	}
	role := sql.NullString{}
	if model.Role_ != "" {
		role.Valid = true
		role.String = string(model.Role_)
	}
	metaJSON, err := json.Marshal(model.Metadata_)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal metadata for folder %v: %v", model.ID_, err))
	}

	var parentID []byte
	if model.ParentID_ != (ulid.ULID{}) {
		parentID = model.ParentID_.Bytes()
	}

	return &FolderDTO{
		ID:          model.ID_,
		ParentID:    parentID,
		AccountID:   model.AccountID_,
		Name:        model.Name_,
		Path:        model.Path_,
		Role:        role,
		Subscribed:  subscribed,
		SortOrder:   model.SortOrder_,
		UIDNext:     model.UIDNext_,
		UIDValidity: model.UIDValidity_,
		Meta:        metaJSON,
		CreatedAt:   model.CreatedAt_,
		UpdatedAt:   model.UpdatedAt_,
	}
}

func AsModel(dto *FolderDTO) *folder.Folder {
	model := &folder.Folder{
		ID_:              dto.ID,
		AccountID_:       dto.AccountID,
		Name_:            dto.Name,
		Path_:            dto.Path,
		Subscribed_:      dto.Subscribed != 0,
		SortOrder_:       dto.SortOrder,
		UIDValidity_:     dto.UIDValidity,
		UIDNext_:         dto.UIDNext,
		CreatedAt_:       dto.CreatedAt,
		UpdatedAt_:       dto.UpdatedAt,
		InitialUpdatedAt: dto.UpdatedAt,
	}

	if dto.Role.Valid {
		model.Role_ = folder.Role(dto.Role.String)
	}

	if err := json.Unmarshal(dto.Meta, &model.Metadata_); err != nil {
		panic(fmt.Sprintf("failed to unmarshal metadata for folder %v: %v", model.ID_, err))
	}

	if len(dto.ParentID) != 0 {
		model.ParentID_ = ulid.ULID(dto.ParentID)
	}

	return model
}

type EntryDTO struct {
	FolderID  ulid.ULID `gorm:"folder_id"`
	MessageID ulid.ULID `gorm:"message_id"`
	UID       uint32    `gorm:"uid"`
}

func (EntryDTO) TableName() string { return "entry" }

func EntryAsDTO(entry *folder.Entry) *EntryDTO {
	return &EntryDTO{
		FolderID:  entry.FolderID_,
		MessageID: entry.MsgID_,
		UID:       entry.UID_,
	}
}

func EntryAsModel(dto *EntryDTO) *folder.Entry {
	return &folder.Entry{
		FolderID_: dto.FolderID,
		MsgID_:    dto.MessageID,
		UID_:      dto.UID,
	}
}
