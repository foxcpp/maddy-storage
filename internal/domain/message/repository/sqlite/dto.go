package messagesqlite

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/domain/message"
	"github.com/oklog/ulid/v2"
)

type msgDTO struct {
	ID              ulid.ULID `gorm:"column:id;primaryKey"`
	ReceivedAt      time.Time `gorm:"column:received_at"`
	TotalSize       uint32    `gorm:"column:total_size"`
	CreatedAtModSeq uint64    `gorm:"column:created_at_modseq"`
	ModSeq          uint64    `gorm:"column:modseq"`
	CreatedAt       time.Time `gorm:"column:created_at;autoCreateTime:false"`
	UpdatedAt       time.Time `gorm:"column:updated_at;autoUpdateTime:false"`
	Meta            []byte    `gorm:"column:meta"`    // JSON
	Content         []byte    `gorm:"column:content"` // JSON
}

func (msgDTO) TableName() string { return "messages" }

type msgFlagDTO struct {
	MessageID ulid.ULID `gorm:"column:message_id;primaryKey"`
	Flag      string    `gorm:"column:flag;primaryKey"`
}

func (msgFlagDTO) TableName() string { return "message_flags" }

type msgPartDTO struct {
	ID             ulid.ULID `gorm:"column:id;primaryKey"`
	MessageID      ulid.ULID `gorm:"column:message_id"`
	Order          int       `gorm:"column:order_"`
	Path           string    `gorm:"column:path"`
	Content        []byte    `gorm:"column:content"` // JSON
	Inline         []byte    `gorm:"column:inline"`  // BLOB
	ExternalBlobID string    `gorm:"column:external_blob_id"`
}

func (msgPartDTO) TableName() string { return "message_parts" }

func asDTO(model *message.Msg) (*msgDTO, []msgFlagDTO, []msgPartDTO, error) {
	metaJson, err := json.Marshal(model.Meta)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to marshal metadata: %v", err)
	}
	contentJson, err := json.Marshal(model.Content)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to marshal content data: %v", err)
	}

	msgDto := &msgDTO{
		ID:              model.ID,
		CreatedAtModSeq: uint64(model.CreatedAtModSeq),
		ModSeq:          uint64(model.ModSeq),
		TotalSize:       model.TotalSize,
		CreatedAt:       model.CreatedAt,
		UpdatedAt:       model.UpdatedAt,
		Meta:            metaJson,
		Content:         contentJson,
	}
	flagsDto := make([]msgFlagDTO, len(model.Flags))
	for i, f := range model.Flags {
		flagsDto[i] = msgFlagDTO{
			MessageID: msgDto.ID,
			Flag:      f,
		}
	}
	partsDto := make([]msgPartDTO, len(model.Parts))
	for i, p := range model.Parts {
		contentJson, err := json.Marshal(p.Content)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to marshal content data: %v", err)
		}

		partsDto[i] = msgPartDTO{
			ID:             p.ID,
			MessageID:      msgDto.ID,
			Order:          p.Order,
			Path:           p.Path.String(),
			Content:        contentJson,
			Inline:         p.Inline,
			ExternalBlobID: p.ExternalBlobID,
		}
	}

	return msgDto, flagsDto, partsDto, nil
}

func asModel(msgDTO *msgDTO, flagsDTO []msgFlagDTO, partsDTO []msgPartDTO) (*message.Msg, error) {
	msg := &message.Msg{
		ID:              msgDTO.ID,
		CreatedAtModSeq: folder.ModSeq(msgDTO.CreatedAtModSeq),
		ModSeq:          folder.ModSeq(msgDTO.ModSeq),
		ReceivedAt:      msgDTO.ReceivedAt,
		CreatedAt:       msgDTO.CreatedAt,
		UpdatedAt:       msgDTO.UpdatedAt,
	}

	if err := json.Unmarshal(msgDTO.Meta, &msg.Meta); err != nil {
		return nil, fmt.Errorf("failed to unmarshal metadata: %v", err)
	}
	if msg.Meta == nil {
		return nil, fmt.Errorf("nil metadata")
	}
	if err := json.Unmarshal(msgDTO.Content, &msg.Content); err != nil {
		return nil, fmt.Errorf("failed to unmarshal msg content: %v", err)
	}
	if msg.Content == nil {
		return nil, fmt.Errorf("nil content")
	}

	msg.Flags = make([]string, len(flagsDTO))
	for i, f := range flagsDTO {
		msg.Flags[i] = f.Flag
	}

	if err := json.Unmarshal(msgDTO.Content, &msg.Content); err != nil {
		return nil, fmt.Errorf("failed to unmarshal content data: %v", err)
	}
	if msg.Content == nil {
		return nil, fmt.Errorf("nil message content data")
	}

	msg.Parts = make([]message.Part, len(partsDTO))
	for i, p := range partsDTO {
		path, err := message.PathFromString(p.Path)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal part %v path: %v", p.ID, err)
		}

		msg.Parts[i] = message.Part{
			ID:             p.ID,
			Order:          p.Order,
			Path:           path,
			Inline:         p.Inline,
			ExternalBlobID: p.ExternalBlobID,
		}

		if err := json.Unmarshal(p.Content, &msg.Parts[i].Content); err != nil {
			return nil, fmt.Errorf("failed to unmarshal part %v content data: %v", p.ID, err)
		}
	}

	return msg, nil
}
