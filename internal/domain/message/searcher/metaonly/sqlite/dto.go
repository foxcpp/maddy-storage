package searchersqlite

import (
	"fmt"
	"time"

	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/domain/message/searcher"
	"github.com/oklog/ulid/v2"
)

// foundRow is a single row of the "return all" query. Fields are matched
// to the projections by the "found_row.<field>" aliases.
type foundRow struct {
	FolderID  []byte
	MessageID []byte
	UID       uint32
	Seq       uint32
	Modseq    int64
	UpdatedAt time.Time
	TotalSize int64
	DeletedAt *time.Time
}

func (r *foundRow) asModel() (searcher.FoundMsg, error) {
	folderID, err := ulidFromBlob(r.FolderID)
	if err != nil {
		return searcher.FoundMsg{}, err
	}
	msgID, err := ulidFromBlob(r.MessageID)
	if err != nil {
		return searcher.FoundMsg{}, err
	}

	m := searcher.FoundMsg{
		FolderID:  folderID,
		UID:       r.UID,
		SeqNum:    r.Seq,
		ModSeq:    folder.ModSeq(r.Modseq),
		MessageID: msgID,
		UpdatedAt: r.UpdatedAt,
		TotalSize: r.TotalSize,
	}
	if r.DeletedAt != nil {
		m.DeletedAt = *r.DeletedAt
	}
	return m, nil
}

// aggregatedRow is a single row of the aggregating query. Fields are matched
// to the projections by the "aggregated_row.<field>" aliases.
type aggregatedRow struct {
	FolderID  []byte
	MinUID    uint32
	MaxUID    uint32
	MinSeq    uint32
	MaxSeq    uint32
	Count     uint32
	MaxModSeq int64
	TotalSize int64
}

func (r *aggregatedRow) asModel() searcher.SearchResult {
	return searcher.SearchResult{
		Count:     r.Count,
		MinUID:    r.MinUID,
		MaxUID:    r.MaxUID,
		MinSeq:    r.MinSeq,
		MaxSeq:    r.MaxSeq,
		TotalSize: r.TotalSize,
		MaxModSeq: folder.ModSeq(r.MaxModSeq),
	}
}

func ulidFromBlob(b []byte) (ulid.ULID, error) {
	var id ulid.ULID
	if len(b) != len(id) {
		return id, fmt.Errorf("invalid ULID length: %d", len(b))
	}
	copy(id[:], b)
	return id, nil
}
