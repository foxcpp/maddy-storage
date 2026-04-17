package searchersql

import (
	"context"
	"database/sql"
	"runtime/trace"
	"strings"
	"time"

	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/domain/folder/recent"
	"github.com/foxcpp/maddy-storage/internal/domain/message/searcher"
	"github.com/foxcpp/maddy-storage/internal/pkg/storeerrors"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlcommon"
	"github.com/oklog/ulid/v2"
	"gorm.io/gorm"
)

type Cfg struct {
	MaxResults int
}

type Searcher struct {
	cfg Cfg
	db  sqlcommon.DB
}

// TODO: This is SQLite specific, will need updates to support PostgreSQL.
const (
	sentDateOnlyExpr  = "COALESCE(NULLIF(substr(json_extract(messages.content, '$.envelope.date'), 1, 10), '0001-01-01'), substr(messages.received_at, 1, 10))"
	sentTimestampExpr = "COALESCE(NULLIF(json_extract(messages.content, '$.envelope.date'), '0001-01-01T00:00:00Z'), messages.received_at)"
)

func New(db sqlcommon.DB, cfg Cfg) *Searcher {
	return &Searcher{
		cfg: cfg,
		db:  db,
	}
}

func (s *Searcher) addConditions(ctx context.Context, q *gorm.DB, filter *searcher.Cond) *gorm.DB {
	if filter == nil {
		return q
	}

	if filter.FolderIDs != nil {
		q = q.Where("folder_entries.folder_id IN (?)", filter.FolderIDs)
	}

	if filter.NumericIDs != nil {
		for _, rang := range filter.NumericIDs {
			q = s.addRangeConds(ctx, q, rang)
		}
	}
	if filter.SentDateOnly {
		if !filter.SentAfter.IsZero() {
			q = q.Where(sentDateOnlyExpr+" >= ?", filter.SentAfter.Format(time.DateOnly))
		}
		if !filter.SentBefore.IsZero() {
			q = q.Where(sentDateOnlyExpr+" < ?", filter.SentBefore.Format(time.DateOnly))
		}
	} else {
		if !filter.SentAfter.IsZero() {
			q = q.Where("unixepoch("+sentTimestampExpr+") > unixepoch(?)", filter.SentAfter)
		}
		if !filter.SentBefore.IsZero() {
			q = q.Where("unixepoch("+sentTimestampExpr+") < unixepoch(?)", filter.SentBefore)
		}
	}
	if filter.ReceivedDateOnly {
		if !filter.ReceivedAfter.IsZero() {
			q = q.Where("date(messages.received_at) >= date(?)", filter.ReceivedAfter)
		}
		if !filter.ReceivedBefore.IsZero() {
			q = q.Where("date(messages.received_at) < date(?)", filter.ReceivedBefore)
		}
	} else {
		if !filter.ReceivedAfter.IsZero() {
			q = q.Where("messages.received_at > ?", filter.ReceivedAfter)
		}
		if !filter.ReceivedBefore.IsZero() {
			q = q.Where("messages.received_at < ?", filter.ReceivedBefore)
		}
	}
	if !filter.UpdatedGt.IsZero() {
		q = q.Where("messages.updated_at > ?", filter.UpdatedGt)
	}
	if !filter.UpdatedLt.IsZero() {
		q = q.Where("messages.updated_at < ?", filter.UpdatedLt)
	}
	if filter.SizeGt != 0 {
		q = q.Where("messages.size > ?", filter.SizeGt)
	}
	if filter.SizeLt != 0 {
		q = q.Where("messages.size < ?", filter.SizeLt)
	}
	if filter.Flag != nil {
		hasRecent, otherFlags := recent.FilterRecentFlag(filter.Flag)
		for i := range otherFlags {
			otherFlags[i] = strings.ToLower(otherFlags[i])
		}
		if len(otherFlags) > 0 {
			q = q.Where(`EXISTS(
				SELECT * FROM message_flags 
				WHERE message_flags.message_id = messages.id
				AND lower(message_flags.flag) IN (?)
				GROUP BY message_flags.message_id
				HAVING count(*) = ?)`, otherFlags, len(otherFlags))
		}
		if hasRecent {
			recentSet, ok := recent.SetFromContext(ctx)
			if ok && !recentSet.Empty() {
				q = q.Where(`folder_entries.uid IN (?)`, recentSet.AsIMAPUIDList())
			} else {
				// No recent messages, so condition is always false.
				q = q.Where("1 = 0")
			}
		}
	}
	if filter.NoFlag != nil {
		hasRecent, otherFlags := recent.FilterRecentFlag(filter.NoFlag)
		for i := range otherFlags {
			otherFlags[i] = strings.ToLower(otherFlags[i])
		}
		if len(otherFlags) > 0 {
			q = q.Where(`NOT EXISTS(
				SELECT * FROM message_flags 
				WHERE message_flags.message_id = messages.id
				AND lower(message_flags.flag) IN (?))`, otherFlags)
		}
		if hasRecent {
			recentSet, ok := recent.SetFromContext(ctx)
			if ok && !recentSet.Empty() {
				q = q.Where(`folder_entries.uid NOT IN (?)`, recentSet.AsIMAPUIDList())
			}
		}
	}
	if filter.Not != nil {
		for _, not := range filter.Not {
			q = q.Not(s.addConditions(ctx, s.db.Gorm(ctx), not))
		}
	}
	if filter.Or != nil {
		for _, or := range filter.Or {
			q = q.Where(
				s.db.Gorm(ctx).
					Where(s.addConditions(ctx, s.db.Gorm(ctx), or[0])).
					Or(s.addConditions(ctx, s.db.Gorm(ctx), or[1])),
			)
		}
	}
	return q
}

func joinSeqNum(q *gorm.DB, at, deletesAt folder.ModSeq, folders ...ulid.ULID) *gorm.DB {
	// XXX: Duplicated in searcher code.
	if at == 0 {
		panic("ranges.At must be set to use ranges.SeqNum or returnSeq")
	}
	if deletesAt == 0 {
		deletesAt = at
	}
	if len(folders) == 0 {
		return q.Joins(`JOIN (
			SELECT folder_entries.folder_id AS folder_id, 
                   folder_entries.uid AS uid,
			       row_number() OVER (PARTITION BY folder_id ORDER BY uid) AS seq
			FROM folder_entries
			JOIN folders ON folders.id = folder_entries.folder_id
			WHERE folder_entries.created_at_modseq <= ? AND (
				folder_entries.deleted_at IS NULL OR
				(folder_entries.deleted_at IS NOT NULL AND folder_entries.modseq > ?))
		) seqnums 
		ON seqnums.msg_id = folder_entries.message_id 
		AND seqnums.folder_id = folder_entries.folder_id`, at, deletesAt)
	} else {
		return q.Joins(`JOIN (
			SELECT folder_entries.folder_id AS folder_id, 
                   folder_entries.uid AS uid,
			       row_number() OVER (PARTITION BY folder_id ORDER BY uid) AS seq
			FROM folder_entries
			JOIN folders ON folders.id = folder_entries.folder_id
			WHERE folder_entries.folder_id IN (?)
			AND folder_entries.created_at_modseq <= ? AND (
				folder_entries.deleted_at IS NULL OR
				(folder_entries.deleted_at IS NOT NULL AND folder_entries.modseq > ?))
		) seqnums 
		ON seqnums.uid = folder_entries.uid 
		AND seqnums.folder_id = folder_entries.folder_id`,
			folders, at, deletesAt)
	}
}

func (s *Searcher) addRangeConds(ctx context.Context, q *gorm.DB, rang folder.Range) *gorm.DB {
	or := s.db.Gorm(ctx)
	addCond := func(query string, args ...any) {
		if or.Statement == nil || len(or.Statement.Clauses) == 0 {
			or = or.Where(query, args...)
			return
		}
		or = or.Or(query, args...)
	}
	if rang.Values != nil {
		if rang.SeqNum {
			addCond("seqnums.seq IN (?)", rang.Values)
		} else {
			addCond("folder_entries.uid IN (?)", rang.Values)
		}
	}
	for _, inter := range rang.Intervals {
		if rang.SeqNum {
			addCond("seqnums.seq BETWEEN ? AND ?", inter.Since, inter.Until)
		} else {
			addCond("folder_entries.uid BETWEEN ? AND ?", inter.Since, inter.Until)
		}
	}
	if or.Statement == nil || len(or.Statement.Clauses) == 0 {
		return q
	}
	return q.Where(or)
}

type foundDTO struct {
	FolderID  ulid.ULID    `gorm:"column:folder_id"`
	MessageID ulid.ULID    `gorm:"column:message_id"`
	UID       uint32       `gorm:"column:uid"`
	Seq       uint32       `gorm:"column:seq"`
	ModSeq    uint64       `gorm:"column:modseq"`
	UpdatedAt time.Time    `gorm:"column:updated_at"`
	TotalSize int64        `gorm:"column:total_size"`
	DeletedAt sql.NullTime `gorm:"column:deleted_at"`
}

type aggregatedDTO struct {
	FolderID     ulid.ULID `gorm:"column:folder_id"`
	MinUID       uint32    `gorm:"column:min_uid"`
	MaxUID       uint32    `gorm:"column:max_uid"`
	MinSeq       uint32    `gorm:"column:min_seq"`
	MaxSeq       uint32    `gorm:"column:max_seq"`
	Count        uint32    `gorm:"column:cnt"`
	MaxUpdatedAt time.Time `gorm:"column:max_updated_at"`
	MaxModSeq    uint64    `gorm:"column:max_modseq"`
	TotalSize    int64     `gorm:"column:total_size"`
}

func (s *Searcher) Search(ctx context.Context, accountID ulid.ULID, cond *searcher.Cond, opts searcher.Opts) (searcher.SearchResult, error) {
	tx := s.db.Gorm(ctx)

	if !opts.ReturnAll {
		res, err := s.searchAggregated(ctx, tx, accountID, cond, opts)
		if err != nil {
			return searcher.SearchResult{}, storeerrors.InternalError{Reason: err}
		}
		return res, nil
	}

	res, err := s.searchAll(ctx, tx, accountID, cond, opts)
	if err != nil {
		return searcher.SearchResult{}, storeerrors.InternalError{Reason: err}
	}
	return res, nil
}

func (s *Searcher) searchAggregated(ctx context.Context, tx *gorm.DB, accountID ulid.ULID, cond *searcher.Cond, opts searcher.Opts) (searcher.SearchResult, error) {
	var selects []string
	if opts.GroupByFolder {
		selects = append(selects, "folder_entries.folder_id AS folder_id")
	}
	if opts.ReturnMinUID {
		selects = append(selects, "min(folder_entries.uid) AS min_uid")
		if opts.ReturnSeqNums {
			selects = append(selects, "min(seqnums.seq) AS min_seq")
		}
	}
	if opts.ReturnMaxUID {
		selects = append(selects, "max(folder_entries.uid) AS max_uid")
		if opts.ReturnSeqNums {
			selects = append(selects, "max(seqnums.seq) AS max_seq")
		}
	}
	if opts.ReturnCount {
		selects = append(selects, "count(folder_entries.uid) AS cnt")
	}
	if opts.ReturnModSeq {
		selects = append(selects, "max(max(folder_entries.modseq), max(messages.modseq)) AS max_modseq")
	}
	if opts.ReturnTotalSize {
		selects = append(selects, "sum(messages.total_size) AS total_size")
	}

	if len(selects) == 0 {
		return searcher.SearchResult{}, nil
	}

	q := tx.
		Select(selects).
		Table("folder_entries").
		Joins("JOIN messages ON messages.id = folder_entries.message_id").
		Joins("JOIN folders ON folders.id = folder_entries.folder_id")

	if cond.NeedsSeqNum() || opts.ReturnSeqNums {
		q = joinSeqNum(q, opts.At, opts.DeletesAt, cond.FolderIDs...)
	} else if opts.At != 0 {
		if opts.DeletesAt == 0 {
			opts.DeletesAt = opts.At
		}
		q = q.Where("folder_entries.modseq <= ?", opts.At).
			Where("(folder_entries.deleted_at IS NULL OR (folder_entries.deleted_at IS NOT NULL AND folder_entries.modseq > ?))", opts.DeletesAt)
	} else {
		q = q.Where("folder_entries.deleted_at IS NULL")
	}

	q = q.Where("folders.account_id = ?", accountID)
	q = s.addConditions(ctx, q, cond)

	if opts.GroupByFolder {
		q = q.Group("folder_entries.folder_id")

		var found []aggregatedDTO
		if err := q.Find(&found).Error; err != nil {
			return searcher.SearchResult{}, storeerrors.InternalError{Reason: err}
		}

		res := searcher.SearchResult{
			ByFolder: make(map[ulid.ULID]*searcher.SearchResult, len(cond.FolderIDs)),
		}

		for _, d := range found {
			res.MinUID = min(res.MinUID, d.MinUID)
			res.MaxUID = min(res.MaxUID, d.MaxUID)
			res.MinSeq = min(res.MinSeq, d.MinSeq)
			res.MaxSeq = min(res.MaxSeq, d.MaxSeq)
			res.Count += d.Count
			if folder.ModSeq(d.MaxModSeq) > res.MaxModSeq {
				res.MaxModSeq = folder.ModSeq(d.MaxModSeq)
			}
			res.TotalSize += d.TotalSize

			res.ByFolder[d.FolderID] = &searcher.SearchResult{
				MinUID:    d.MinUID,
				MaxUID:    d.MaxUID,
				MinSeq:    d.MinSeq,
				MaxSeq:    d.MaxSeq,
				Count:     d.Count,
				MaxModSeq: folder.ModSeq(d.MaxModSeq),
				TotalSize: d.TotalSize,
			}
		}
	}

	var found aggregatedDTO
	if err := q.First(&found).Error; err != nil {
		return searcher.SearchResult{}, storeerrors.InternalError{Reason: err}
	}

	return searcher.SearchResult{
		Count:     found.Count,
		MinUID:    found.MinUID,
		MaxUID:    found.MaxUID,
		MaxModSeq: folder.ModSeq(found.MaxModSeq),
	}, nil
}

func (s *Searcher) searchAll(ctx context.Context, tx *gorm.DB, accountID ulid.ULID, cond *searcher.Cond, opts searcher.Opts) (searcher.SearchResult, error) {
	defer trace.StartRegion(ctx, "maddy-storage/message.searcher.metaonly.sqlcommon.Search").End()

	// We don't bother using DB to aggregate results as we will have all of them
	// in memory anyway.
	selects := []string{
		"folder_entries.folder_id AS folder_id",
		"folder_entries.message_id AS message_id",
		"folder_entries.uid AS uid",
		"max(folder_entries.modseq, messages.modseq) AS modseq",
		"messages.updated_at AS updated_at",
		"messages.total_size AS total_size",
		"folder_entries.deleted_at AS deleted_at",
	}
	if opts.ReturnSeqNums {
		selects = append(selects, "seqnums.seq AS seq")
	}
	q := tx.
		Select(selects).
		Table("folder_entries").
		Joins("JOIN messages ON messages.id = folder_entries.message_id").
		Joins("JOIN folders ON folders.id = folder_entries.folder_id")

	if cond.NeedsSeqNum() || opts.ReturnSeqNums {
		q = joinSeqNum(q, opts.At, opts.DeletesAt, cond.FolderIDs...)
	} else if opts.At != 0 {
		if opts.DeletesAt == 0 {
			opts.DeletesAt = opts.At
		}
		q = q.Where("folder_entries.created_at_modseq <= ?", opts.At).
			Where("(folder_entries.deleted_at IS NULL OR (folder_entries.deleted_at IS NOT NULL AND folder_entries.modseq > ?))", opts.DeletesAt)
	} else {
		q = q.Where("folder_entries.deleted_at IS NULL")
	}

	q = q.Where("folders.account_id = ?", accountID)
	q = s.addConditions(ctx, q, cond)
	q = s.addOrder(q, opts.Sort)
	if s.cfg.MaxResults != 0 {
		q = q.Limit(s.cfg.MaxResults)
	}

	var dtos []foundDTO
	if err := q.Find(&dtos).Error; err != nil {
		return searcher.SearchResult{}, err
	}

	res := searcher.SearchResult{
		All: make([]searcher.FoundMsg, 0, len(dtos)),
	}
	if opts.ReturnCount {
		res.Count = uint32(len(dtos))
	}

	for _, d := range dtos {
		var deletedAt time.Time
		if d.DeletedAt.Valid {
			deletedAt = d.DeletedAt.Time
		}

		res.Add(&opts, searcher.FoundMsg{
			FolderID:  d.FolderID,
			UID:       d.UID,
			SeqNum:    d.Seq,
			MessageID: d.MessageID,
			UpdatedAt: d.UpdatedAt,
			TotalSize: d.TotalSize,
			DeletedAt: deletedAt,
		})
	}

	return res, nil
}

func (s *Searcher) SearchFlag(ctx context.Context, accountID, folderID ulid.ULID, flag string, opts searcher.Opts) (searcher.SearchResult, error) {
	return s.Search(ctx, accountID, &searcher.Cond{
		FolderIDs: []ulid.ULID{folderID},
		Flag:      []string{flag},
	}, opts)
}

func (s *Searcher) SearchWithoutFlag(ctx context.Context, accountID, folderID ulid.ULID, flag string, opts searcher.Opts) (searcher.SearchResult, error) {
	return s.Search(ctx, accountID, &searcher.Cond{
		FolderIDs: []ulid.ULID{folderID},
		NoFlag:    []string{flag},
	}, opts)
}

func (s *Searcher) SearchFolder(ctx context.Context, accountID, folderID ulid.ULID, opts searcher.Opts) (searcher.SearchResult, error) {
	return s.Search(ctx, accountID, &searcher.Cond{
		FolderIDs: []ulid.ULID{folderID},
	}, opts)
}

func (s *Searcher) Tx(ctx context.Context, inTx func(ctx context.Context, s searcher.Searcher) error) error {
	return s.db.Tx(ctx, true, func(tx sqlcommon.DB) error {
		return inTx(ctx, &Searcher{
			cfg: s.cfg,
			db:  tx,
		})
	})
}
