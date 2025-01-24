package searcher

import (
	"context"
	"time"

	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/oklog/ulid/v2"
)

type FoundMsg struct {
	FolderID  ulid.ULID
	SeqNum    uint32 // requires Opts.ReturnSeqNums
	UID       uint32
	ModSeq    folder.ModSeq
	MessageID ulid.ULID
	UpdatedAt time.Time
	TotalSize int64
	DeletedAt time.Time // May be set if used with Opts.At/DeletesAt, means that the message was actually removed.
}

type Opts struct {
	ReturnAll       bool
	ReturnCount     bool
	ReturnMaxUID    bool
	ReturnMinUID    bool
	ReturnTotalSize bool
	ReturnModSeq    bool
	GroupByFolder   bool // Return aggregated results per folder.

	// Calculate and return per-folder message sequence numbers
	// based on folder state at given point in time.
	ReturnSeqNums bool
	At            folder.ModSeq
	DeletesAt     folder.ModSeq

	Sort []SortKey // Only used with ReturnAll
}

type SearchResult struct {
	All            []FoundMsg
	Count          uint32
	MinUID, MaxUID uint32
	MinSeq, MaxSeq uint32
	TotalSize      int64
	MaxModSeq      folder.ModSeq

	ByFolder map[ulid.ULID]*SearchResult
}

func (res *SearchResult) Add(opts *Opts, ent FoundMsg) {
	res.add(opts, ent)
	if opts.GroupByFolder {
		if res.ByFolder == nil {
			res.ByFolder = make(map[ulid.ULID]*SearchResult)
		}

		subRes := res.ByFolder[ent.FolderID]
		if subRes == nil {
			subRes = &SearchResult{}
			res.ByFolder[ent.FolderID] = subRes
		}
		subRes.add(opts, ent)
	}
}

func (res *SearchResult) add(opts *Opts, ent FoundMsg) {
	if opts.ReturnAll {
		res.All = append(res.All, ent)
	}
	if opts.ReturnMaxUID && (res.MaxUID < ent.UID || res.MaxUID == 0) {
		res.MaxUID = ent.UID
		res.MaxSeq = ent.SeqNum
	}
	if opts.ReturnMinUID && (res.MinUID > ent.UID || res.MinUID == 0) {
		res.MinUID = ent.UID
		res.MinSeq = ent.SeqNum
	}
	if opts.ReturnModSeq && (res.MaxModSeq < ent.ModSeq) || res.MaxModSeq == 0 {
		res.MaxModSeq = ent.ModSeq
	}
	if opts.ReturnTotalSize {
		res.TotalSize += ent.TotalSize
	}
}

type Searcher interface {
	Search(ctx context.Context, accountID ulid.ULID, cond *Cond, opts Opts) (SearchResult, error)

	// Optimized shortcuts for IMAP4rev1.

	SearchFolder(ctx context.Context, accountID, folderID ulid.ULID, opts Opts) (SearchResult, error)
	SearchFlag(ctx context.Context, accountID, folderID ulid.ULID, flag string, opts Opts) (SearchResult, error)
	SearchWithoutFlag(ctx context.Context, accountID, folderID ulid.ULID, flag string, opts Opts) (SearchResult, error)

	Tx(ctx context.Context, inTx func(ctx context.Context, s Searcher) error) error
}
