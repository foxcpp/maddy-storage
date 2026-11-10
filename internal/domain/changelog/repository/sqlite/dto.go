package changelogsqlite

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/foxcpp/maddy-storage/internal/domain/changelog"
	"github.com/foxcpp/maddy-storage/internal/domain/metadata"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite/jetdb/model"
	"github.com/oklog/ulid/v2"
)

func asDTO(ent *changelog.Entry) (*model.ChangelogEntries, error) {
	md := ent.Metadata
	if md == nil {
		md = metadata.New()
	}
	meta, err := json.Marshal(md)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal metadata for entry %v: %w", ent.At, err)
	}

	var payload any
	switch {
	case ent.Message != nil:
		payload = ent.Message
	case ent.Folder != nil:
		payload = ent.Folder
	case ent.Account != nil:
		payload = ent.Account
	}

	var data *[]byte
	if payload != nil {
		d, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal data for entry %v: %w", ent.At, err)
		}
		data = &d
	}

	return &model.ChangelogEntries{
		At:        ent.At,
		Type:      string(ent.Type),
		AccountID: ent.AccountID[:],
		FolderID:  optBlob(ent.FolderID),
		MessageID: optBlob(ent.MessageID),
		Meta:      meta,
		Data:      data,
	}, nil
}

func asModel(dto *model.ChangelogEntries) (*changelog.Entry, error) {
	ent := &changelog.Entry{
		At:       dto.At,
		Type:     changelog.Type(dto.Type),
		Metadata: metadata.New(),
	}

	var err error
	if ent.AccountID, err = ulidFromBlob(dto.AccountID); err != nil {
		return nil, err
	}
	if dto.FolderID != nil {
		if ent.FolderID, err = ulidFromBlob(*dto.FolderID); err != nil {
			return nil, err
		}
	}
	if dto.MessageID != nil {
		if ent.MessageID, err = ulidFromBlob(*dto.MessageID); err != nil {
			return nil, err
		}
	}

	if err := json.Unmarshal(dto.Meta, &ent.Metadata); err != nil {
		return nil, fmt.Errorf("failed to unmarshal metadata for entry %v: %w", dto.At, err)
	}

	if dto.Data == nil {
		return ent, nil
	}

	var target any
	switch prefix, _, _ := strings.Cut(dto.Type, "."); prefix {
	case "account":
		ent.Account = &changelog.AccountEntry{}
		target = ent.Account
	case "folder":
		ent.Folder = &changelog.FolderEntry{}
		target = ent.Folder
	case "message":
		ent.Message = &changelog.MessageEntry{}
		target = ent.Message
	default:
		return ent, nil
	}
	if err := json.Unmarshal(*dto.Data, target); err != nil {
		return nil, fmt.Errorf("failed to unmarshal %s data for entry %v: %w", dto.Type, dto.At, err)
	}

	return ent, nil
}

// optBlob maps the zero ULID ("not applicable") to NULL.
func optBlob(id ulid.ULID) *[]byte {
	if id == (ulid.ULID{}) {
		return nil
	}
	b := id[:]
	return &b
}

func ulidFromBlob(b []byte) (ulid.ULID, error) {
	var id ulid.ULID
	if len(b) != len(id) {
		return id, fmt.Errorf("invalid ULID length: %d", len(b))
	}
	copy(id[:], b)
	return id, nil
}
