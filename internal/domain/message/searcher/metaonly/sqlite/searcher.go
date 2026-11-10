package searchersqlite

import (
	"context"
	"runtime/trace"
	"strings"
	"time"

	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/domain/folder/recent"
	"github.com/foxcpp/maddy-storage/internal/domain/message/searcher"
	"github.com/foxcpp/maddy-storage/internal/pkg/storeerrors"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlcommon"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite/jetdb/table"
	j "github.com/go-jet/jet/v2/sqlite"
	"github.com/oklog/ulid/v2"
)

type Cfg struct {
	MaxResults int
}

type Searcher struct {
	cfg Cfg
	db  sqlcommon.DB
}

var (
	sentDateOnlyExpr = j.StringExp(j.COALESCE(
		j.NULLIF(
			j.SUBSTR(j.StringExp(j.Func("json_extract", table.Messages.Content, j.String("$.envelope.date"))), j.Int(1), j.Int(10)),
			j.String("0001-01-01"),
		),
		j.SUBSTR(j.StringExp(table.Messages.ReceivedAt), j.Int(1), j.Int(10)),
		j.String("0001-01-01"),
	))
	sentTimestampExpr = j.TimestampExp(j.COALESCE(
		j.NULLIF(
			j.Func("json_extract", table.Messages.Content, j.String("$.envelope.date")),
			j.String("0001-01-01T00:00:00Z"),
		),
		table.Messages.ReceivedAt,
	))
)

func New(db sqlcommon.DB, cfg Cfg) *Searcher {
	return &Searcher{
		cfg: cfg,
		db:  db,
	}
}

func blob(id ulid.ULID) j.BlobExpression {
	return j.Blob(id[:])
}

func blobs(ids []ulid.ULID) []j.Expression {
	res := make([]j.Expression, len(ids))
	for i, id := range ids {
		res[i] = blob(id)
	}
	return res
}

func ints[T ~uint32 | ~int](vals []T) []j.Expression {
	res := make([]j.Expression, len(vals))
	for i, v := range vals {
		res[i] = j.Int(int64(v))
	}
	return res
}

// andExpression joins conditions, returns nil if there are none.
func andExpression(conds []j.BoolExpression) j.BoolExpression {
	if len(conds) == 0 {
		return nil
	}

	res := conds[0]
	for _, c := range conds[1:] {
		res = res.AND(c)
	}
	return res
}

// visibleAt limits entries to the ones existing at the account state
// identified by "at" and "deletesAt" modseqs.
func visibleAt(e *table.FolderEntriesTable, at, deletesAt folder.ModSeq) j.BoolExpression {
	// XXX: Duplicated in folder repository code.

	if deletesAt == 0 {
		deletesAt = at
	}

	return e.CreatedAtModseq.LT_EQ(j.Int(int64(at))).
		AND(e.DeletedAt.IS_NULL().OR(e.Modseq.GT(j.Int(int64(deletesAt)))))
}

// seqNums returns a sub-query numbering entries of each folder the way they
// were visible at the specified state. Exposed columns are "folder_id", "uid"
// and "seq". If folders is empty, all folders are numbered.
func seqNums(at, deletesAt folder.ModSeq, folders []ulid.ULID) (sub j.SelectTable, folderID j.ColumnBlob, uid, seq j.ColumnInteger) {
	// XXX: Duplicated in folder repository code.
	if at == 0 {
		panic("ranges.At must be set to use ranges.SeqNum or returnSeq")
	}

	e := table.FolderEntries.AS("seqnums_entries")

	where := visibleAt(e, at, deletesAt)
	if len(folders) != 0 {
		where = e.FolderID.IN(blobs(folders)...).AND(where)
	}

	sub = j.SELECT(
		e.FolderID,
		e.UID,
		j.ROW_NUMBER().OVER(j.PARTITION_BY(e.FolderID).ORDER_BY(e.UID)).AS("seq"),
	).
		FROM(e).
		WHERE(where).
		AsTable("seqnums")

	return sub, e.FolderID.From(sub), e.UID.From(sub), j.IntegerColumn("seq").From(sub)
}

// rangeConds returns nil if the range does not limit anything.
func rangeConds(rang folder.Range, seq j.ColumnInteger) j.BoolExpression {
	col := table.FolderEntries.UID
	if rang.SeqNum {
		col = seq
	}

	var conds []j.BoolExpression
	if len(rang.Values) != 0 {
		conds = append(conds, col.IN(ints(rang.Values)...))
	}
	for _, inter := range rang.Intervals {
		conds = append(conds, col.BETWEEN(j.Int(int64(inter.Since)), j.Int(int64(inter.Until))))
	}

	switch {
	case len(conds) == 0 && rang.Values != nil:
		// Explicitly empty set matches nothing. Do not rely on "IN (NULL)"
		// here as it is NULL (not false) and so survives no negation.
		return j.Bool(false)
	case len(conds) == 0:
		return nil
	}

	cond := conds[0]
	for _, c := range conds[1:] {
		cond = cond.OR(c)
	}
	return cond
}

func flagsInCond(flags []string) j.BoolExpression {
	lower := make([]j.Expression, len(flags))
	for i, f := range flags {
		lower[i] = j.String(strings.ToLower(f))
	}
	return j.LOWER(table.MessageFlags.Flag).IN(lower...)
}

func hasAllFlags(flags []string) j.BoolExpression {
	return j.EXISTS(
		j.SELECT(j.Int(1)).
			FROM(table.MessageFlags).
			WHERE(table.MessageFlags.MessageID.EQ(table.Messages.ID).
				AND(flagsInCond(flags))).
			GROUP_BY(table.MessageFlags.MessageID).
			HAVING(j.COUNT(j.STAR).EQ(j.Int(int64(len(flags))))),
	)
}

func hasAnyFlag(flags []string) j.BoolExpression {
	return j.EXISTS(
		j.SELECT(j.Int(1)).
			FROM(table.MessageFlags).
			WHERE(table.MessageFlags.MessageID.EQ(table.Messages.ID).
				AND(flagsInCond(flags))),
	)
}

// joinConds translates the condition to the list of ANDed expressions, seq is the
// sequence number column of the query (nil if not joined).
func joinConds(ctx context.Context, filter *searcher.Cond, seqNumColumn j.ColumnInteger) []j.BoolExpression {
	if filter == nil {
		return nil
	}

	var res []j.BoolExpression

	if filter.FolderIDs != nil {
		if len(filter.FolderIDs) == 0 {
			res = append(res, j.Bool(false))
		} else {
			res = append(res, table.FolderEntries.FolderID.IN(blobs(filter.FolderIDs)...))
		}
	}

	for _, rang := range filter.NumericIDs {
		if c := rangeConds(rang, seqNumColumn); c != nil {
			res = append(res, c)
		}
	}

	if filter.SentDateOnly {
		if !filter.SentAfter.IsZero() {
			res = append(res, sentDateOnlyExpr.GT_EQ(j.String(filter.SentAfter.Format(time.DateOnly))))
		}
		if !filter.SentBefore.IsZero() {
			res = append(res, sentDateOnlyExpr.LT(j.String(filter.SentBefore.Format(time.DateOnly))))
		}
	} else {
		if !filter.SentAfter.IsZero() {
			res = append(res,
				j.IntExp(j.Func("unixepoch", sentTimestampExpr)).
					GT(j.IntExp(j.Func("unixepoch", sqlite.Timestamp(filter.SentAfter)))),
			)
		}
		if !filter.SentBefore.IsZero() {
			res = append(res,
				j.IntExp(j.Func("unixepoch", sentTimestampExpr)).
					LT(j.IntExp(j.Func("unixepoch", sqlite.Timestamp(filter.SentBefore)))),
			)
		}
	}

	if filter.ReceivedDateOnly {
		if !filter.ReceivedAfter.IsZero() {
			res = append(res, j.DATE(table.Messages.ReceivedAt).GT_EQ(j.DATE(sqlite.Timestamp(filter.ReceivedAfter))))
		}
		if !filter.ReceivedBefore.IsZero() {
			res = append(res, j.DATE(table.Messages.ReceivedAt).LT(j.DATE(sqlite.Timestamp(filter.ReceivedBefore))))
		}
	} else {
		if !filter.ReceivedAfter.IsZero() {
			res = append(res, table.Messages.ReceivedAt.GT(sqlite.Timestamp(filter.ReceivedAfter)))
		}
		if !filter.ReceivedBefore.IsZero() {
			res = append(res, table.Messages.ReceivedAt.LT(sqlite.Timestamp(filter.ReceivedBefore)))
		}
	}

	if !filter.UpdatedGt.IsZero() {
		res = append(res, table.Messages.UpdatedAt.GT(sqlite.Timestamp(filter.UpdatedGt)))
	}
	if !filter.UpdatedLt.IsZero() {
		res = append(res, table.Messages.UpdatedAt.LT(sqlite.Timestamp(filter.UpdatedLt)))
	}
	if filter.SizeGt != 0 {
		res = append(res, table.Messages.TotalSize.GT(j.Int(int64(filter.SizeGt))))
	}
	if filter.SizeLt != 0 {
		res = append(res, table.Messages.TotalSize.LT(j.Int(int64(filter.SizeLt))))
	}

	if filter.Flag != nil {
		hasRecent, otherFlags := recent.FilterRecentFlag(filter.Flag)
		if len(otherFlags) > 0 {
			res = append(res, hasAllFlags(otherFlags))
		}
		if hasRecent {
			uids := recentUIDs(ctx)
			if len(uids) != 0 {
				res = append(res, table.FolderEntries.UID.IN(ints(uids)...))
			} else {
				// No recent messages, so condition is always false.
				res = append(res, j.Bool(false))
			}
		}
	}
	if filter.NoFlag != nil {
		hasRecent, otherFlags := recent.FilterRecentFlag(filter.NoFlag)
		if len(otherFlags) > 0 {
			res = append(res, j.NOT(hasAnyFlag(otherFlags)))
		}
		if hasRecent {
			if uids := recentUIDs(ctx); len(uids) != 0 {
				res = append(res, table.FolderEntries.UID.NOT_IN(ints(uids)...))
			}
		}
	}

	for _, not := range filter.Not {
		// XXX: NOT of a condition that does not limit anything is ignored, it
		// should match nothing instead.
		if c := andExpression(joinConds(ctx, not, seqNumColumn)); c != nil {
			res = append(res, j.NOT(c))
		}
	}

	for _, or := range filter.Or {
		a, b := andExpression(joinConds(ctx, or[0], seqNumColumn)), andExpression(joinConds(ctx, or[1], seqNumColumn))
		// An operand that does not limit anything matches everything, so does
		// the OR.
		if a != nil && b != nil {
			res = append(res, a.OR(b))
		}
	}

	return res
}

func recentUIDs(ctx context.Context) []int {
	set, ok := recent.SetFromContext(ctx)
	if !ok || set.Empty() {
		return nil
	}
	return set.AsIMAPUIDList()
}

// query is the part shared by all searches: entries joined with their
// messages andExpression folders, limited to the account andExpression visible at the requested
// state.
type query struct {
	from  j.ReadableTable
	where j.BoolExpression
	seq   j.ColumnInteger // nil unless sequence numbers are joined
}

func newQuery(ctx context.Context, accountID ulid.ULID, cond *searcher.Cond, opts searcher.Opts) query {
	e := table.FolderEntries

	q := query{
		from: e.
			INNER_JOIN(table.Messages, table.Messages.ID.EQ(e.MessageID)).
			INNER_JOIN(table.Folders, table.Folders.ID.EQ(e.FolderID)),
		where: table.Folders.AccountID.EQ(blob(accountID)),
	}

	switch {
	case cond.NeedsSeqNum() || opts.ReturnSeqNums:
		sub, subFolder, subUID, subSeq := seqNums(opts.At, opts.DeletesAt, cond.FolderIDs)
		q.from = q.from.INNER_JOIN(sub, subUID.EQ(e.UID).AND(subFolder.EQ(e.FolderID)))
		q.seq = subSeq
	case opts.At != 0:
		q.where = q.where.AND(visibleAt(e, opts.At, opts.DeletesAt))
	default:
		q.where = q.where.AND(e.DeletedAt.IS_NULL())
	}

	for _, c := range joinConds(ctx, cond, q.seq) {
		q.where = q.where.AND(c)
	}

	return q
}

func (s *Searcher) Search(ctx context.Context, accountID ulid.ULID, cond *searcher.Cond, opts searcher.Opts) (searcher.SearchResult, error) {
	var (
		res searcher.SearchResult
		err error
	)
	if opts.ReturnAll {
		res, err = s.searchAll(ctx, accountID, cond, opts)
	} else {
		res, err = s.searchAggregated(ctx, accountID, cond, opts)
	}
	if err != nil {
		return searcher.SearchResult{}, storeerrors.InternalError{Reason: err}
	}

	return res, nil
}

func (s *Searcher) searchAggregated(ctx context.Context, accountID ulid.ULID, cond *searcher.Cond, opts searcher.Opts) (searcher.SearchResult, error) {
	const alias = "aggregated_row."

	e := table.FolderEntries

	var proj []j.Projection
	if opts.GroupByFolder {
		proj = append(proj, e.FolderID.AS(alias+"folder_id"))
	}
	q := newQuery(ctx, accountID, cond, opts)
	if opts.ReturnMinUID {
		proj = append(proj, j.MIN(e.UID).AS(alias+"min_uid"))
		if opts.ReturnSeqNums {
			proj = append(proj, j.MIN(q.seq).AS(alias+"min_seq"))
		}
	}
	if opts.ReturnMaxUID {
		proj = append(proj, j.MAX(e.UID).AS(alias+"max_uid"))
		if opts.ReturnSeqNums {
			proj = append(proj, j.MAX(q.seq).AS(alias+"max_seq"))
		}
	}
	if opts.ReturnCount {
		proj = append(proj, j.COUNT(e.UID).AS(alias+"count"))
	}
	if opts.ReturnModSeq {
		proj = append(proj, j.Func("max", j.MAX(e.Modseq), j.MAX(table.Messages.Modseq)).AS(alias+"max_mod_seq"))
	}
	if opts.ReturnTotalSize {
		proj = append(proj, j.SUM(table.Messages.TotalSize).AS(alias+"total_size"))
	}

	if len(proj) == 0 {
		return searcher.SearchResult{}, nil
	}

	stmt := j.SELECT(proj[0], proj[1:]...).
		FROM(q.from).
		WHERE(q.where)

	if !opts.GroupByFolder {
		var found aggregatedRow
		if err := s.db.Query(ctx, stmt, &found); err != nil {
			return searcher.SearchResult{}, err
		}
		return found.asModel(), nil
	}

	var found []aggregatedRow
	if err := s.db.Query(ctx, stmt.GROUP_BY(e.FolderID), &found); err != nil {
		return searcher.SearchResult{}, err
	}

	res := searcher.SearchResult{
		ByFolder: make(map[ulid.ULID]*searcher.SearchResult, len(cond.FolderIDs)),
	}
	// GROUP BY does not return rows for folders without matches, but callers
	// expect a (zero) result for each requested folder.
	for _, id := range cond.FolderIDs {
		res.ByFolder[id] = &searcher.SearchResult{}
	}

	// Zero means "unset" for min values, so it must not win the comparison.
	minNonZero := func(a, b uint32) uint32 {
		if a == 0 {
			return b
		}
		if b == 0 {
			return a
		}
		return min(a, b)
	}

	for i := range found {
		d := found[i].asModel()
		id, err := ulidFromBlob(found[i].FolderID)
		if err != nil {
			return searcher.SearchResult{}, err
		}

		res.MinUID = minNonZero(res.MinUID, d.MinUID)
		res.MaxUID = max(res.MaxUID, d.MaxUID)
		res.MinSeq = minNonZero(res.MinSeq, d.MinSeq)
		res.MaxSeq = max(res.MaxSeq, d.MaxSeq)
		res.Count += d.Count
		res.MaxModSeq = max(res.MaxModSeq, d.MaxModSeq)
		res.TotalSize += d.TotalSize

		res.ByFolder[id] = &d
	}

	return res, nil
}

func (s *Searcher) searchAll(ctx context.Context, accountID ulid.ULID, cond *searcher.Cond, opts searcher.Opts) (searcher.SearchResult, error) {
	defer trace.StartRegion(ctx, "maddy-storage/message.searcher.metaonly.sqlite.Search").End()

	const alias = "found_row."

	e := table.FolderEntries
	q := newQuery(ctx, accountID, cond, opts)

	// We don't bother using DB to aggregate results as we will have all of them
	// in memory anyway.
	proj := []j.Projection{
		e.FolderID.AS(alias + "folder_id"),
		e.MessageID.AS(alias + "message_id"),
		e.UID.AS(alias + "uid"),
		j.Func("max", e.Modseq, table.Messages.Modseq).AS(alias + "modseq"),
		table.Messages.UpdatedAt.AS(alias + "updated_at"),
		table.Messages.TotalSize.AS(alias + "total_size"),
		e.DeletedAt.AS(alias + "deleted_at"),
	}
	if opts.ReturnSeqNums {
		proj = append(proj, q.seq.AS(alias+"seq"))
	}

	stmt := j.SELECT(proj[0], proj[1:]...).
		FROM(q.from).
		WHERE(q.where).
		ORDER_BY(orderClauses(opts.Sort)...)
	if s.cfg.MaxResults != 0 {
		stmt = stmt.LIMIT(int64(s.cfg.MaxResults))
	}

	var rows []foundRow
	if err := s.db.Query(ctx, stmt, &rows); err != nil {
		return searcher.SearchResult{}, err
	}

	res := searcher.SearchResult{
		All: make([]searcher.FoundMsg, 0, len(rows)),
	}
	for i := range rows {
		m, err := rows[i].asModel()
		if err != nil {
			return searcher.SearchResult{}, err
		}
		res.Add(&opts, m)
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
