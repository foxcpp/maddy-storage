package messagesqlite

import (
	"encoding/json"
	"fmt"

	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/domain/message"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite/jetdb/model"
	"github.com/oklog/ulid/v2"
)

func asDTO(m *message.Msg) (*model.Messages, []model.MessageFlags, []model.MessageParts, error) {
	metaJSON, err := json.Marshal(m.Meta)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to marshal metadata: %w", err)
	}
	contentJSON, err := json.Marshal(m.Content)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to marshal content data: %w", err)
	}

	msg := &model.Messages{
		ID: m.ID[:],
		// IMAP internal date, there is no separate field in message.Msg.
		Date:            m.ReceivedAt,
		ReceivedAt:      m.ReceivedAt,
		TotalSize:       int64(m.TotalSize),
		Modseq:          int64(m.ModSeq),
		CreatedAtModseq: int64(m.CreatedAtModSeq),
		CreatedAt:       m.CreatedAt,
		UpdatedAt:       m.UpdatedAt,
		Meta:            metaJSON,
		Content:         contentJSON,
	}

	flags := flagsAsDTO(m.ID, m.Flags)

	parts := make([]model.MessageParts, len(m.Parts))
	for i, p := range m.Parts {
		partJSON, err := json.Marshal(p.Content)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to marshal part content data: %w", err)
		}

		parts[i] = model.MessageParts{
			ID:        p.ID[:],
			MessageID: m.ID[:],
			Order:     int64(p.Order),
			Path:      p.Path.String(),
			Content:   partJSON,
			Inline:    nilIfNil(p.Inline),
		}
		if p.ExternalBlobID != "" {
			parts[i].ExternalBlobID = &p.ExternalBlobID
		}
	}

	return msg, flags, parts, nil
}

func flagsAsDTO(id ulid.ULID, flags []string) []model.MessageFlags {
	dtos := make([]model.MessageFlags, len(flags))
	for i, f := range flags {
		dtos[i] = model.MessageFlags{MessageID: id[:], Flag: f}
	}
	return dtos
}

// nilIfNil maps nil slice to NULL, empty non-nil slice is stored as is.
func nilIfNil(b []byte) *[]byte {
	if b == nil {
		return nil
	}
	return &b
}

func asModel(dto *model.Messages, flags []model.MessageFlags, parts []model.MessageParts) (*message.Msg, error) {
	id, err := ulidFromBlob(dto.ID)
	if err != nil {
		return nil, err
	}

	msg := &message.Msg{
		ID:              id,
		CreatedAtModSeq: folder.ModSeq(dto.CreatedAtModseq),
		ModSeq:          folder.ModSeq(dto.Modseq),
		ReceivedAt:      dto.ReceivedAt,
		TotalSize:       uint32(dto.TotalSize),
		CreatedAt:       dto.CreatedAt,
		UpdatedAt:       dto.UpdatedAt,
	}

	if err := json.Unmarshal(dto.Meta, &msg.Meta); err != nil {
		return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
	}
	if msg.Meta == nil {
		return nil, fmt.Errorf("nil metadata")
	}
	if err := json.Unmarshal(dto.Content, &msg.Content); err != nil {
		return nil, fmt.Errorf("failed to unmarshal msg content: %w", err)
	}
	if msg.Content == nil {
		return nil, fmt.Errorf("nil message content data")
	}

	msg.Flags = make([]string, len(flags))
	for i, f := range flags {
		msg.Flags[i] = f.Flag
	}

	msg.Parts = make([]message.Part, len(parts))
	for i, p := range parts {
		partID, err := ulidFromBlob(p.ID)
		if err != nil {
			return nil, err
		}
		path, err := message.PathFromString(p.Path)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal part %v path: %w", partID, err)
		}

		msg.Parts[i] = message.Part{
			ID:    partID,
			Order: int(p.Order),
			Path:  path,
		}
		if p.Inline != nil {
			msg.Parts[i].Inline = *p.Inline
		}
		if p.ExternalBlobID != nil {
			msg.Parts[i].ExternalBlobID = *p.ExternalBlobID
		}

		if err := json.Unmarshal(p.Content, &msg.Parts[i].Content); err != nil {
			return nil, fmt.Errorf("failed to unmarshal part %v content data: %w", partID, err)
		}
	}

	return msg, nil
}

func ulidFromBlob(b []byte) (ulid.ULID, error) {
	var id ulid.ULID
	if len(b) != len(id) {
		return id, fmt.Errorf("invalid ULID length: %d", len(b))
	}
	copy(id[:], b)
	return id, nil
}
