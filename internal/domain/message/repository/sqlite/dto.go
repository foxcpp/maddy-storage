package messagesqlite

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/foxcpp/maddy-storage/internal/domain/message"
	"github.com/oklog/ulid/v2"
)

type msgDTO struct {
	ID        ulid.ULID `gorm:"id,primaryKey"`
	Date      time.Time `gorm:"date"`
	CreatedAt time.Time `gorm:"created_at,autoCreateTime:false"`
	UpdatedAt time.Time `gorm:"updated_at,autoUpdateTime:false"`
	Meta      []byte    `gorm:"meta"`    // JSON
	Content   []byte    `gorm:"content"` // JSON
}

func (msgDTO) TableName() string { return "messages" }

type msgFlagDTO struct {
	MsgID [16]byte `gorm:"message_id"`
	Flag  string   `gorm:"flag"`
}

func (msgFlagDTO) TableName() string { return "message_flags" }

type msgPartDTO struct {
	ID             ulid.ULID `gorm:"id,primaryKey"`
	MessageID      ulid.ULID `gorm:"message_id"`
	Path           string    `gorm:"path"`
	Content        []byte    `gorm:"content"` // JSON
	Inline         []byte    `gorm:"inline"`  // BLOB
	ExternalBlobID string    `gorm:"external_blob_id"`
}

func (msgPartDTO) TableName() string { return "message_parts" }

func asDTO(model *message.Msg) (*msgDTO, []msgFlagDTO, []msgPartDTO, error) {
	metaJson, err := json.Marshal(model.Meta_)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to marshal metadata: %v", err)
	}
	contentJson, err := json.Marshal(model.Content_)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to marshal content data: %v", err)
	}

	msgDto := &msgDTO{
		ID:        model.ID_,
		Date:      model.ReceivedAt_,
		CreatedAt: model.CreatedAt_,
		UpdatedAt: model.UpdatedAt_,
		Meta:      metaJson,
		Content:   contentJson,
	}
	flagsDto := make([]msgFlagDTO, len(model.Flags_))
	for i, f := range model.Flags_ {
		flagsDto[i] = msgFlagDTO{
			MsgID: msgDto.ID,
			Flag:  f,
		}
	}
	partsDto := make([]msgPartDTO, len(model.Parts_))
	for i, p := range model.Parts_ {
		contentJson, err := json.Marshal(p.Content_)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to marshal content data: %v", err)
		}

		partsDto[i] = msgPartDTO{
			ID:             p.ID_,
			MessageID:      msgDto.ID,
			Path:           p.Path_.String(),
			Content:        contentJson,
			Inline:         p.Inline_,
			ExternalBlobID: p.ExternalBlobID_,
		}
	}

	return msgDto, flagsDto, partsDto, nil
}

func asModel(msgDTO *msgDTO, flagsDTO []msgFlagDTO, partsDTO []msgPartDTO) (*message.Msg, error) {
	msg := &message.Msg{
		ID_:         msgDTO.ID,
		ReceivedAt_: msgDTO.Date,
		CreatedAt_:  msgDTO.CreatedAt,
		UpdatedAt_:  msgDTO.UpdatedAt,
	}

	if err := json.Unmarshal(msgDTO.Meta, &msg.Meta_); err != nil {
		return nil, fmt.Errorf("failed to unmarshal metadata: %v", err)
	}
	if msg.Content_ == nil {
		return nil, fmt.Errorf("nil metadata")
	}

	msg.Flags_ = make([]string, len(flagsDTO))
	for i, f := range flagsDTO {
		msg.Flags_[i] = f.Flag
	}

	if err := json.Unmarshal(msgDTO.Content, &msg.Content_); err != nil {
		return nil, fmt.Errorf("failed to unmarshal content data: %v", err)
	}
	if msg.Content_ == nil {
		return nil, fmt.Errorf("nil message content data")
	}

	msg.Parts_ = make([]message.Part, len(partsDTO))
	for i, p := range partsDTO {
		path, err := message.PathFromString(p.Path)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal part %v path: %v", p.ID, err)
		}

		msg.Parts_[i] = message.Part{
			ID_:             p.ID,
			Path_:           path,
			Inline_:         p.Inline,
			ExternalBlobID_: p.ExternalBlobID,
		}

		if err := json.Unmarshal(p.Content, &msg.Parts_[i].Content_); err != nil {
			return nil, fmt.Errorf("failed to unmarshal part %v content data: %v", p.ID, err)
		}
	}

	return msg, nil
}
