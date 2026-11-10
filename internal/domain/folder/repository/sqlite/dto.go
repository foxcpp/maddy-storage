package foldersqlite

import (
	"encoding/json"
	"fmt"

	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite/jetdb/model"
	"github.com/oklog/ulid/v2"
)

// entryRow is folder_entries plus the sequence number computed by the
// "seqnums" sub-query, see seqNums.
type entryRow struct {
	model.FolderEntries

	// Projected as "entry_row.seq".
	Seq uint32
}

func asDTO(f *folder.Folder) (*model.Folders, error) {
	meta, err := json.Marshal(f.Metadata_)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal metadata for folder %v: %w", f.ID, err)
	}

	var role *string
	if f.Role != folder.RoleNone {
		r := string(f.Role)
		role = &r
	}
	subscribed := int64(0)
	if f.Subscribed {
		subscribed = 1
	}
	var parentID *[]byte
	if f.ParentID != (ulid.ULID{}) {
		p := f.ParentID[:]
		parentID = &p
	}

	return &model.Folders{
		ID:         f.ID[:],
		ParentID:   parentID,
		AccountID:  f.AccountID[:],
		Name:       f.Name,
		Path:       f.Path,
		Role:       role,
		Subscribed: subscribed,
		SortOrder:  int64(f.SortOrder),
		Meta:       meta,
		CreatedAt:  f.CreatedAt,
		UpdatedAt:  f.UpdatedAt,
	}, nil
}

func asModel(dto *model.Folders) (*folder.Folder, error) {
	f, err := ulidFromBlob(dto.ID)
	if err != nil {
		return nil, err
	}
	acctID, err := ulidFromBlob(dto.AccountID)
	if err != nil {
		return nil, err
	}

	fld := &folder.Folder{
		ID:               f,
		AccountID:        acctID,
		Name:             dto.Name,
		Path:             dto.Path,
		Subscribed:       dto.Subscribed != 0,
		SortOrder:        uint(dto.SortOrder),
		CreatedAt:        dto.CreatedAt,
		UpdatedAt:        dto.UpdatedAt,
		InitialUpdatedAt: dto.UpdatedAt,
	}

	if dto.ParentID != nil {
		parentID, err := ulidFromBlob(*dto.ParentID)
		if err != nil {
			return nil, err
		}
		fld.ParentID = parentID
	}
	if dto.Role != nil {
		fld.Role = folder.Role(*dto.Role)
	}
	if dto.Meta != nil {
		if err := json.Unmarshal(dto.Meta, &fld.Metadata_); err != nil {
			return nil, fmt.Errorf("failed to unmarshal metadata for folder %v: %w", fld.ID, err)
		}
	}

	return fld, nil
}

func entryAsDTO(entry *folder.Entry) model.FolderEntries {
	msgID := entry.MsgID[:]

	return model.FolderEntries{
		FolderID:        entry.FolderID[:],
		MessageID:       &msgID,
		UID:             int64(entry.IMAPUID),
		Modseq:          int64(entry.ModSeq),
		CreatedAtModseq: int64(entry.CreatedAtModSeq),
		CreatedAt:       entry.CreatedAt,
	}
}

func entryAsModel(row *entryRow) (*folder.Entry, error) {
	folderID, err := ulidFromBlob(row.FolderID)
	if err != nil {
		return nil, err
	}

	entry := &folder.Entry{
		FolderID:        folderID,
		IMAPUID:         uint32(row.UID),
		CreatedAtModSeq: folder.ModSeq(row.CreatedAtModseq),
		ModSeq:          folder.ModSeq(row.Modseq),
		SeqNum:          row.Seq,
		CreatedAt:       row.CreatedAt,
	}

	// message_id is set to NULL by ON DELETE SET NULL when the message is
	// removed.
	if row.MessageID != nil {
		msgID, err := ulidFromBlob(*row.MessageID)
		if err != nil {
			return nil, err
		}
		entry.MsgID = msgID
	}
	if row.DeletedAt != nil {
		entry.DeletedAt = *row.DeletedAt
	}

	return entry, nil
}

func entriesAsModels(rows []entryRow) ([]folder.Entry, error) {
	entries := make([]folder.Entry, len(rows))
	for i := range rows {
		e, err := entryAsModel(&rows[i])
		if err != nil {
			return nil, err
		}
		entries[i] = *e
	}
	return entries, nil
}

func ulidFromBlob(b []byte) (ulid.ULID, error) {
	var id ulid.ULID
	if len(b) != len(id) {
		return id, fmt.Errorf("invalid ULID length: %d", len(b))
	}
	copy(id[:], b)
	return id, nil
}
