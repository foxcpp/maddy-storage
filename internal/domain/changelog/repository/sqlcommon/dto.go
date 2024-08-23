package sqlcommon

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/foxcpp/maddy-storage/internal/domain/changelog"
	"github.com/foxcpp/maddy-storage/internal/domain/metadata"
	"github.com/oklog/ulid/v2"
)

type EntryDTO struct {
	At        time.Time `gorm:"at"`
	Type      string    `gorm:"type"`
	AccountID ulid.ULID `gorm:"account_id"`
	FolderID  ulid.ULID `gorm:"folder_id"`
	MessageID ulid.ULID `gorm:"message_id"`
	Meta      []byte    `gorm:"meta"` // JSON
	Data      []byte    `gorm:"data"` // JSON
}

func (EntryDTO) TableName() string { return "changelog_entry" }

func AsDTO(ent *changelog.Entry) *EntryDTO {
	dto := &EntryDTO{
		At:        ent.At,
		Type:      string(ent.Type),
		AccountID: ent.AccountID,
		FolderID:  ent.FolderID,
		MessageID: ent.MessageID,
		Meta:      nil,
		Data:      nil,
	}

	var err error
	dto.Meta, err = json.Marshal(ent.Metadata)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal metadata for entry %v: %v", ent.At, err))
	}

	if ent.Account != nil {
		dto.Data, err = json.Marshal(ent.Account)
	}
	if ent.Folder != nil {
		dto.Data, err = json.Marshal(ent.Folder)
	}
	if ent.Message != nil {
		dto.Data, err = json.Marshal(ent.Message)
	}
	if err != nil {
		panic(fmt.Sprintf("failed to marshal data for entry %v: %v", ent.At, err))
	}

	return dto
}

func AsModel(dto *EntryDTO) *changelog.Entry {
	ent := &changelog.Entry{
		At:        dto.At,
		Type:      changelog.Type(dto.Type),
		AccountID: dto.AccountID,
		FolderID:  dto.FolderID,
		MessageID: dto.MessageID,
		Account:   nil,
		Folder:    nil,
		Message:   nil,
	}

	ent.Metadata = metadata.New()
	if err := json.Unmarshal(dto.Meta, &ent.Metadata); err != nil {
		panic(fmt.Sprintf("failed to unmarshal metadata for entry %v: %v", dto.At, err))
	}

	switch prefix, _, _ := strings.Cut(dto.Type, "."); prefix {
	case "account":
		ent.Account = &changelog.AccountEntry{}
		if err := json.Unmarshal(dto.Data, &ent.Account); err != nil {
			panic(fmt.Sprintf("failed to unmarshal account data for entry %v: %v", dto.At, err))
		}
	case "folder":
		ent.Folder = &changelog.FolderEntry{}
		if err := json.Unmarshal(dto.Data, &ent.Folder); err != nil {
			panic(fmt.Sprintln("failed to unmarshal folder data for entry: ", err))
		}
	case "message":
		ent.Message = &changelog.MessageEntry{}
		if err := json.Unmarshal(dto.Data, &ent.Message); err != nil {
			panic(fmt.Sprintln("failed to unmarshal message data for entry: ", err))
		}
	default:
	}

	return ent
}
