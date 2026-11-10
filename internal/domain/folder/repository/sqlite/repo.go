package foldersqlite

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"runtime/trace"
	"sort"
	"strings"
	"time"

	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/pkg/contextlib"
	"github.com/foxcpp/maddy-storage/internal/pkg/storeerrors"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlcommon"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite/jetdb/model"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite/jetdb/table"
	"github.com/go-jet/jet/v2/qrm"
	j "github.com/go-jet/jet/v2/sqlite"
	"github.com/oklog/ulid/v2"
)

type repo struct {
	db       sqlcommon.DB
	executor sqlcommon.Tx
}

func New(db sqlcommon.DB) folder.Repo {
	return repo{
		db:       db,
		executor: db,
	}
}

var _ folder.Repo = &repo{}

var likeEscape = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func (r repo) inTx(ctx context.Context, readOnly bool, fn func(r repo) error) error {
	if r.db == nil {
		contextlib.Logger(ctx).Error("nested transactions not supported, reusing outer transaction")
		return fn(r)
	}

	return r.db.Tx(ctx, readOnly, func(tx sqlcommon.Tx) error {
		return fn(repo{
			// db: nil  To prevent accidental query leak outside of used transaction.
			executor: tx,
		})
	})
}

func filterConds(f *folder.Filter) []j.BoolExpression {
	// regexp is handled in GetByAccount

	var conds []j.BoolExpression

	if f.NameContains != nil {
		conds = append(conds, sqlite.LikeEscape(table.Folders.Name, "%"+likeEscape.Replace(*f.NameContains)+"%", `\`))
	}
	if f.Path != nil {
		conds = append(conds, table.Folders.Path.EQ(j.String(*f.Path)))
	}
	if f.PathPrefix != nil {
		conds = append(conds, sqlite.LikeEscape(table.Folders.Path, likeEscape.Replace(*f.PathPrefix)+"%", `\`))
	}
	if f.ParentID != nil {
		conds = append(conds, table.Folders.ParentID.EQ(sqlite.ULID(*f.ParentID)))
	}
	if f.ParentPath != nil {
		conds = append(conds, sqlite.LikeEscape(table.Folders.Path, likeEscape.Replace(*f.ParentPath)+folder.PathSeparator+"%", `\`))
	}
	if f.Subscribed != nil {
		subNum := int64(0)
		if *f.Subscribed {
			subNum = 1
		}
		conds = append(conds, table.Folders.Subscribed.EQ(j.Int(subNum)))
	}
	if f.HasRole != nil {
		if *f.HasRole {
			conds = append(conds, table.Folders.Role.IS_NOT_NULL())
		} else {
			conds = append(conds, table.Folders.Role.IS_NULL())
		}
	}
	if f.Role != nil {
		conds = append(conds, table.Folders.Role.EQ(j.String(string(*f.Role))))
	}

	return conds
}

func accountWhere(accountID ulid.ULID, f *folder.Filter, extra ...j.BoolExpression) j.BoolExpression {
	where := table.Folders.AccountID.EQ(sqlite.ULID(accountID))
	for _, c := range extra {
		where = where.AND(c)
	}
	for _, c := range filterConds(f) {
		where = where.AND(c)
	}
	return where
}

func orderKey(o folder.Order) []j.OrderByClause {
	switch o {
	case folder.OrderBySortOrder:
		return []j.OrderByClause{table.Folders.SortOrder.ASC(), table.Folders.Name.ASC()}
	case folder.OrderBySortOrderDesc:
		return []j.OrderByClause{table.Folders.SortOrder.ASC(), table.Folders.Name.DESC()}
	case folder.OrderByCreatedAt:
		return []j.OrderByClause{table.Folders.CreatedAt.ASC(), table.Folders.Name.ASC()}
	case folder.OrderByCreatedAtDesc:
		return []j.OrderByClause{table.Folders.CreatedAt.DESC(), table.Folders.Name.DESC()}
	case folder.OrderByName:
		return []j.OrderByClause{table.Folders.Name.ASC()}
	case folder.OrderByNameDesc:
		return []j.OrderByClause{table.Folders.Name.DESC()}
	default:
		panic("unknown sort order")
	}
}

func foldersAsModels(dtos []model.Folders) ([]folder.Folder, error) {
	models := make([]folder.Folder, len(dtos))
	for i := range dtos {
		m, err := asModel(&dtos[i])
		if err != nil {
			return nil, err
		}
		models[i] = *m
	}
	return models, nil
}

func (r repo) getOne(ctx context.Context, where j.BoolExpression) (*folder.Folder, error) {
	var dto model.Folders

	stmt := j.SELECT(table.Folders.AllColumns).
		FROM(table.Folders).
		WHERE(where).
		LIMIT(1)
	if err := r.executor.Query(ctx, stmt, &dto); err != nil {
		if errors.Is(err, qrm.ErrNoRows) {
			return nil, folder.ErrNotFound
		}
		return nil, storeerrors.InternalError{Reason: err}
	}

	m, err := asModel(&dto)
	if err != nil {
		return nil, storeerrors.InternalError{Reason: err}
	}
	return m, nil
}

func (r repo) GetByID(ctx context.Context, id ulid.ULID) (*folder.Folder, error) {
	defer trace.StartRegion(ctx, "maddy-storage/folder.repository.sqlite.GetByID").End()

	return r.getOne(ctx, table.Folders.ID.EQ(sqlite.ULID(id)))
}

func (r repo) GetByPath(ctx context.Context, accountID ulid.ULID, path string) (*folder.Folder, error) {
	defer trace.StartRegion(ctx, "maddy-storage/folder.repository.sqlite.GetByPath").End()

	return r.getOne(ctx, table.Folders.AccountID.EQ(sqlite.ULID(accountID)).
		AND(table.Folders.Path.EQ(j.String(path))))
}

func (r repo) getByRegexp(ctx context.Context, accountID ulid.ULID, f folder.Filter, order folder.Order, regex *regexp.Regexp) ([]folder.Folder, error) {
	prefix, complete := regex.LiteralPrefix()

	var prefixCond j.BoolExpression
	if complete && isAnchored(regex.String()) {
		// The pattern names exactly one path, "path =" can use the unique
		// index while LIKE cannot. LiteralPrefix ignores the anchors, so the
		// "complete" flag alone is not enough (it is true for "^abc$").
		prefixCond = table.Folders.Path.EQ(j.String(prefix))
	} else {
		prefixCond = sqlite.LikeEscape(table.Folders.Path, likeEscape.Replace(prefix)+"%", `\`)
	}

	var dtos []model.Folders
	stmt := j.SELECT(table.Folders.AllColumns).
		FROM(table.Folders).
		WHERE(accountWhere(accountID, &f, prefixCond)).
		ORDER_BY(orderKey(order)...)
	if err := r.executor.Query(ctx, stmt, &dtos); err != nil {
		return nil, err
	}

	models, err := foldersAsModels(dtos)
	if err != nil {
		return nil, err
	}

	filtered := models[:0]
	for _, m := range models {
		if regex.MatchString(m.Path) {
			filtered = append(filtered, m)
		}
	}
	return filtered, nil
}

// isAnchored reports whether the pattern starts with "^" and ends with "$"
// (unescaped).
func isAnchored(pattern string) bool {
	if !strings.HasPrefix(pattern, "^") || !strings.HasSuffix(pattern, "$") {
		return false
	}

	backslashes := 0
	for i := len(pattern) - 2; i >= 0 && pattern[i] == '\\'; i-- {
		backslashes++
	}
	return backslashes%2 == 0
}

func (r repo) GetByAccount(ctx context.Context, accountID ulid.ULID, f folder.Filter, order folder.Order) ([]folder.Folder, error) {
	defer trace.StartRegion(ctx, "maddy-storage/folder.repository.sqlite.GetByAccount").End()

	var (
		models []folder.Folder
		err    error
	)
	switch len(f.PathRegex) {
	case 0:
		var dtos []model.Folders
		stmt := j.SELECT(table.Folders.AllColumns).
			FROM(table.Folders).
			WHERE(accountWhere(accountID, &f)).
			ORDER_BY(orderKey(order)...)
		if err = r.executor.Query(ctx, stmt, &dtos); err == nil {
			models, err = foldersAsModels(dtos)
		}
	case 1:
		models, err = r.getByRegexp(ctx, accountID, f, order, f.PathRegex[0])
	default:
		// A folder matching several patterns must be returned once.
		seen := make(map[ulid.ULID]struct{})
		err = r.inTx(ctx, true, func(tx repo) error {
			for _, re := range f.PathRegex {
				found, err := tx.getByRegexp(ctx, accountID, f, order, re)
				if err != nil {
					return err
				}
				for _, m := range found {
					if _, ok := seen[m.ID]; ok {
						continue
					}
					seen[m.ID] = struct{}{}
					models = append(models, m)
				}
			}
			return nil
		})
	}
	if err != nil {
		return nil, storeerrors.InternalError{Reason: err}
	}

	if models == nil {
		models = []folder.Folder{}
	}
	return models, nil
}

func (r repo) CountByAccount(ctx context.Context, accountID ulid.ULID, f folder.Filter) (int, error) {
	defer trace.StartRegion(ctx, "maddy-storage/folder.repository.sqlite.CountByAccount").End()

	if len(f.PathRegex) == 0 {
		var dest struct{ Count int64 }
		stmt := j.SELECT(j.COUNT(j.STAR).AS("count")).
			FROM(table.Folders).
			WHERE(accountWhere(accountID, &f))
		if err := r.executor.Query(ctx, stmt, &dest); err != nil {
			return 0, storeerrors.InternalError{Reason: err}
		}
		return int(dest.Count), nil
	}

	// Regexps are matched in Go, so folders have to be fetched anyway.
	found, err := r.GetByAccount(ctx, accountID, f, folder.OrderByCreatedAt)
	if err != nil {
		return 0, err
	}
	return len(found), nil
}

func (r repo) GetByPrefix(ctx context.Context, accountID ulid.ULID, f folder.Filter, prefixes ...string) ([]folder.Folder, error) {
	defer trace.StartRegion(ctx, "maddy-storage/folder.repository.sqlite.GetByPrefix").End()

	found := make(map[ulid.ULID]folder.Folder)

	err := r.inTx(ctx, true, func(tx repo) error {
		for _, p := range prefixes {
			prefixCond := table.Folders.Path.EQ(j.String(p)).
				OR(sqlite.LikeEscape(table.Folders.Path, likeEscape.Replace(p)+folder.PathSeparator+"%", `\`))

			var dtos []model.Folders
			stmt := j.SELECT(table.Folders.AllColumns).
				FROM(table.Folders).
				WHERE(accountWhere(accountID, &f, prefixCond))
			if err := tx.executor.Query(ctx, stmt, &dtos); err != nil {
				return err
			}

			models, err := foldersAsModels(dtos)
			if err != nil {
				return err
			}
			for _, m := range models {
				found[m.ID] = m
			}
		}
		return nil
	})
	if err != nil {
		return nil, storeerrors.InternalError{Reason: err}
	}

	models := make([]folder.Folder, 0, len(found))
	for _, m := range found {
		models = append(models, m)
	}
	sort.Slice(models, func(i, j int) bool {
		return models[i].CreatedAt.Before(models[j].CreatedAt)
	})

	return models, nil
}

func (r repo) Create(ctx context.Context, f *folder.Folder) error {
	defer trace.StartRegion(ctx, "maddy-storage/folder.repository.sqlite.Create").End()

	dto, err := asDTO(f)
	if err != nil {
		return storeerrors.InternalError{Reason: err}
	}

	stmt := table.Folders.INSERT(table.Folders.AllColumns).MODEL(dto)
	if _, err := r.executor.Exec(ctx, stmt); err != nil {
		if sqlite.IsUniqueConstraintError(err) {
			return folder.ErrAlreadyExists
		}
		if sqlite.IsForeignConstraintError(err) {
			if f.ParentID == (ulid.ULID{}) {
				return storeerrors.LogicError{Text: "user account does not exist"}
			}
			return storeerrors.LogicError{Text: "parent folder does not exist"}
		}
		return storeerrors.InternalError{Reason: err}
	}

	return nil
}

func (r repo) Update(ctx context.Context, f *folder.Folder) error {
	defer trace.StartRegion(ctx, "maddy-storage/folder.repository.sqlite.Update").End()

	dto, err := asDTO(f)
	if err != nil {
		return storeerrors.InternalError{Reason: err}
	}

	stmt := table.Folders.UPDATE(table.Folders.Subscribed, table.Folders.Role).
		MODEL(dto).
		WHERE(table.Folders.ID.EQ(sqlite.ULID(f.ID)).
			AND(table.Folders.UpdatedAt.EQ(sqlite.Timestamp(f.InitialUpdatedAt))))
	res, err := r.executor.Exec(ctx, stmt)
	if err != nil {
		return storeerrors.InternalError{Reason: err}
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return folder.ErrNotFound // XXX: Figure out a way to differentiate OCC failure from missing object
	}

	return nil
}

func (r repo) Delete(ctx context.Context, folderID ulid.ULID) error {
	defer trace.StartRegion(ctx, "maddy-storage/folder.repository.sqlite.Delete").End()

	stmt := table.Folders.DELETE().WHERE(table.Folders.ID.EQ(sqlite.ULID(folderID)))
	if _, err := r.executor.Exec(ctx, stmt); err != nil {
		if sqlite.IsForeignConstraintError(err) {
			return folder.ErrHasChildren
		}
		return storeerrors.InternalError{Reason: err}
	}

	return nil
}

func (r repo) RenameMove(
	ctx context.Context, accountID ulid.ULID,
	oldParent, newParent *folder.Folder,
	oldName, newName string,
) ([]folder.RenamedFolder, error) {
	defer trace.StartRegion(ctx, "maddy-storage/folder.repository.sqlite.RenameMove").End()

	oldPath, newPath := oldName, newName
	oldParentCond := table.Folders.ParentID.IS_NULL()
	newParentID := j.BlobExp(j.NULL)
	if oldParent != nil {
		oldPath = oldParent.Path + folder.PathSeparator + oldName
		oldParentCond = table.Folders.ParentID.EQ(sqlite.ULID(oldParent.ID))
	}
	if newParent != nil {
		newPath = newParent.Path + folder.PathSeparator + newName
		newParentID = sqlite.ULID(newParent.ID)
	}

	var data []model.Folders

	err := r.inTx(ctx, false, func(tx repo) error {
		// 1. Change parent, update path and name.
		var moved []model.Folders
		stmt := table.Folders.UPDATE().
			SET(
				table.Folders.ParentID.SET(newParentID),
				table.Folders.Path.SET(j.String(newPath)),
				table.Folders.Name.SET(j.String(newName)),
			).
			WHERE(table.Folders.AccountID.EQ(sqlite.ULID(accountID)). // Constraints below are redundant for consistency.
											AND(oldParentCond).
											AND(table.Folders.Path.EQ(j.String(oldPath))).
											AND(table.Folders.Name.EQ(j.String(oldName)))).
			RETURNING(table.Folders.ID, table.Folders.Path)
		if err := tx.executor.Query(ctx, stmt, &moved); err != nil {
			if sqlite.IsUniqueConstraintError(err) {
				return folder.ErrAlreadyExists
			}
			if sqlite.IsForeignConstraintError(err) {
				return storeerrors.NotExistsError{Text: "parent folder does not exist"}
			}
			return storeerrors.InternalError{Reason: err}
		}
		if len(moved) == 0 {
			return folder.ErrNotFound
		}

		// 2. Update path for children directories (parent_id stays the same).
		children := make([]model.Folders, 0, len(moved))
		stmt = table.Folders.UPDATE().
			SET(table.Folders.Path.SET(
				j.String(newPath).CONCAT(j.SUBSTR(table.Folders.Path, j.Int(int64(len(oldPath)+1)))),
			)).
			WHERE(table.Folders.AccountID.EQ(sqlite.ULID(accountID)).
				AND(sqlite.LikeEscape(table.Folders.Path, likeEscape.Replace(oldPath)+folder.PathSeparator+"%", `\`))).
			RETURNING(table.Folders.ID, table.Folders.Path)
		if err := tx.executor.Query(ctx, stmt, &children); err != nil {
			if sqlite.IsUniqueConstraintError(err) { // Pretty much should be impossible, but check just in case.
				return folder.ErrAlreadyExists
			}
			return storeerrors.InternalError{Reason: err}
		}

		data = append(children, moved...)
		return nil
	})
	if err != nil {
		return nil, err
	}

	res := make([]folder.RenamedFolder, len(data))
	for i, f := range data {
		id, err := ulidFromBlob(f.ID)
		if err != nil {
			return nil, storeerrors.InternalError{Reason: err}
		}
		res[i] = folder.RenamedFolder{
			ID:      id,
			OldPath: strings.Replace(f.Path, newPath, oldPath, 1),
			NewPath: f.Path,
		}
	}

	return res, nil
}

func (r repo) DeleteTree(ctx context.Context, accountID ulid.ULID, root string) ([]folder.DeletedFolder, error) {
	defer trace.StartRegion(ctx, "maddy-storage/folder.repository.sqlite.DeleteTree").End()

	var data []model.Folders

	stmt := table.Folders.DELETE().
		WHERE(table.Folders.AccountID.EQ(sqlite.ULID(accountID)).
			AND(table.Folders.Path.EQ(j.String(root)).
				OR(sqlite.LikeEscape(table.Folders.Path, likeEscape.Replace(root)+folder.PathSeparator+"%", `\`)))).
		RETURNING(table.Folders.ID, table.Folders.Path)
	if err := r.executor.Query(ctx, stmt, &data); err != nil {
		return nil, storeerrors.InternalError{Reason: err}
	}

	deleted := make([]folder.DeletedFolder, len(data))
	for i, d := range data {
		id, err := ulidFromBlob(d.ID)
		if err != nil {
			return nil, storeerrors.InternalError{Reason: err}
		}
		deleted[i] = folder.DeletedFolder{ID: id, Path: d.Path}
	}

	return deleted, nil
}

// visibleAt limits entries to the ones existing at the account state
// identified by "at" and "deletesAt" modseqs.
func visibleAt(e *table.FolderEntriesTable, at, deletesAt folder.ModSeq) j.BoolExpression {
	// XXX: Duplicated in searcher code.

	if deletesAt == 0 {
		deletesAt = at
	}

	return e.CreatedAtModseq.LT_EQ(j.Int(int64(at))).
		AND(e.DeletedAt.IS_NULL().OR(e.Modseq.GT(j.Int(int64(deletesAt)))))
}

// seqNums returns a sub-query numbering entries of the folder the way they
// were visible at the specified state. Exposed columns are "folder_id", "uid"
// and "seq".
func seqNums(folderID ulid.ULID, at, deletesAt folder.ModSeq) (j.SelectTable, j.ColumnBlob, j.ColumnInteger, j.ColumnInteger) {
	// XXX: Duplicated in searcher code.
	if at == 0 {
		panic("ranges.At must be set to use ranges.SeqNum or returnSeq")
	}

	e := table.FolderEntries.AS("seqnums_entries")
	sub := j.SELECT(
		e.FolderID,
		e.UID,
		j.ROW_NUMBER().OVER(j.PARTITION_BY(e.FolderID).ORDER_BY(e.UID)).AS("seq"),
	).
		FROM(e).
		WHERE(e.FolderID.EQ(sqlite.ULID(folderID)).AND(visibleAt(e, at, deletesAt))).
		AsTable("seqnums")

	return sub, e.FolderID.From(sub), e.UID.From(sub), j.IntegerColumn("seq").From(sub)
}

// entriesQuery describes the part of query shared by everything that selects
// folder entries by range.
type entriesQuery struct {
	from  j.ReadableTable
	where j.BoolExpression
	seq   j.ColumnInteger // only if the sequence numbers are joined
}

func newEntriesQuery(folderID ulid.ULID, ranges folder.Range, withSeq bool) entriesQuery {
	e := table.FolderEntries

	q := entriesQuery{
		from:  e,
		where: e.FolderID.EQ(sqlite.ULID(folderID)),
	}

	switch {
	case withSeq:
		sub, subFolder, subUID, subSeq := seqNums(folderID, ranges.At, ranges.DeletesAt)
		q.from = e.INNER_JOIN(sub, subUID.EQ(e.UID).AND(subFolder.EQ(e.FolderID)))
		q.seq = subSeq
	case ranges.At != 0:
		q.where = q.where.AND(visibleAt(e, ranges.At, ranges.DeletesAt))
	default:
		q.where = q.where.AND(e.DeletedAt.IS_NULL())
	}

	if cond := rangeConds(ranges, q.seq); cond != nil {
		q.where = q.where.AND(cond)
	}

	return q
}

// rangeConds returns nil if the range does not limit anything.
func rangeConds(rang folder.Range, seq j.ColumnInteger) j.BoolExpression {
	col := table.FolderEntries.UID
	if rang.SeqNum {
		col = seq
	}

	var conds []j.BoolExpression
	if len(rang.Values) != 0 {
		vals := make([]j.Expression, len(rang.Values))
		for i, v := range rang.Values {
			vals[i] = j.Int(int64(v))
		}
		conds = append(conds, col.IN(vals...))
	}
	for _, inter := range rang.Intervals {
		conds = append(conds, col.BETWEEN(j.Int(int64(inter.Since)), j.Int(int64(inter.Until))))
	}

	switch {
	case len(conds) == 0 && rang.Values != nil:
		// Explicitly empty set matches nothing.
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

func (r repo) CreateEntry(ctx context.Context, entry ...folder.Entry) error {
	defer trace.StartRegion(ctx, "maddy-storage/folder.repository.sqlite.CreateEntry").End()

	if len(entry) == 0 {
		return nil
	}

	dtos := make([]model.FolderEntries, len(entry))
	for i := range entry {
		dtos[i] = entryAsDTO(&entry[i])
	}

	stmt := table.FolderEntries.INSERT(table.FolderEntries.AllColumns).MODELS(dtos)
	if _, err := r.executor.Exec(ctx, stmt); err != nil {
		if sqlite.IsForeignConstraintError(err) {
			return folder.ErrDanglingEntry
		}
		return storeerrors.InternalError{Reason: err}
	}

	return nil
}

func (r repo) ReplaceEntries(ctx context.Context, old []folder.Entry, new []folder.Entry, modSeq folder.ModSeq) error {
	defer trace.StartRegion(ctx, "maddy-storage/folder.repository.sqlite.ReplaceEntries").End()

	return r.inTx(ctx, false, func(tx repo) error {
		for _, ent := range old {
			stmt := table.FolderEntries.UPDATE().
				SET(
					table.FolderEntries.DeletedAt.SET(sqlite.Timestamp(time.Now())),
					table.FolderEntries.Modseq.SET(j.Int(int64(modSeq))),
				).
				WHERE(table.FolderEntries.FolderID.EQ(sqlite.ULID(ent.FolderID)).
					AND(table.FolderEntries.MessageID.EQ(sqlite.ULID(ent.MsgID))).
					AND(table.FolderEntries.DeletedAt.IS_NULL()))
			if _, err := tx.executor.Exec(ctx, stmt); err != nil {
				return err
			}
		}

		return tx.CreateEntry(ctx, new...)
	})
}

func (r repo) selectEntries(ctx context.Context, q entriesQuery) ([]folder.Entry, error) {
	proj := j.ProjectionList{table.FolderEntries.AllColumns}
	if q.seq != nil {
		proj = append(proj, q.seq.AS("entry_row.seq"))
	}

	var rows []entryRow
	stmt := j.SELECT(proj[0], proj[1:]...).
		FROM(q.from).
		WHERE(q.where).
		ORDER_BY(table.FolderEntries.UID)
	if err := r.executor.Query(ctx, stmt, &rows); err != nil {
		return nil, fmt.Errorf("find: %w", err)
	}

	return entriesAsModels(rows)
}

func (r repo) GetEntryByRange(ctx context.Context, folderID ulid.ULID, ranges folder.Range, returnSeq bool) ([]folder.Entry, error) {
	defer trace.StartRegion(ctx, "maddy-storage/folder.repository.sqlite.GetEntryByRange").End()

	return r.selectEntries(ctx, newEntriesQuery(folderID, ranges, ranges.SeqNum || returnSeq))
}

func (r repo) GetEntryByIDs(ctx context.Context, folderID ulid.ULID, msgIDs []ulid.ULID) ([]folder.Entry, error) {
	defer trace.StartRegion(ctx, "maddy-storage/folder.repository.sqlite.GetEntryByIDs").End()

	if len(msgIDs) == 0 {
		return []folder.Entry{}, nil
	}

	return r.selectEntries(ctx, entriesQuery{
		from: table.FolderEntries,
		where: table.FolderEntries.FolderID.EQ(sqlite.ULID(folderID)).
			AND(table.FolderEntries.MessageID.IN(sqlite.ULIDList(msgIDs)...)).
			AND(table.FolderEntries.DeletedAt.IS_NULL()),
	})
}

func (r repo) CountEntryByRange(ctx context.Context, folderID ulid.ULID, ranges folder.Range) (int, error) {
	defer trace.StartRegion(ctx, "maddy-storage/folder.repository.sqlite.CountEntryByRange").End()

	q := newEntriesQuery(folderID, ranges, ranges.SeqNum)

	var dest struct{ Count int64 }
	stmt := j.SELECT(j.COUNT(j.STAR).AS("count")).
		FROM(q.from).
		WHERE(q.where)
	if err := r.executor.Query(ctx, stmt, &dest); err != nil {
		return 0, err
	}

	return int(dest.Count), nil
}

func (r repo) DeleteEntryByIDs(ctx context.Context, folderID ulid.ULID, ids []ulid.ULID, modSeq folder.ModSeq) ([]folder.Entry, error) {
	defer trace.StartRegion(ctx, "maddy-storage/folder.repository.sqlite.DeleteEntryByIDs").End()

	if len(ids) == 0 {
		return []folder.Entry{}, nil
	}

	var rows []entryRow
	stmt := table.FolderEntries.UPDATE().
		SET(
			table.FolderEntries.DeletedAt.SET(sqlite.Timestamp(time.Now())),
			table.FolderEntries.Modseq.SET(j.Int(int64(modSeq))),
		).
		WHERE(table.FolderEntries.FolderID.EQ(sqlite.ULID(folderID)).
			AND(table.FolderEntries.MessageID.IN(sqlite.ULIDList(ids)...)).
			AND(table.FolderEntries.DeletedAt.IS_NULL())).
		RETURNING(table.FolderEntries.AllColumns)
	if err := r.executor.Query(ctx, stmt, &rows); err != nil {
		return nil, err
	}

	return entriesAsModels(rows)
}

func (r repo) DeleteEntryByRange(ctx context.Context, folderID ulid.ULID, ranges folder.Range, returnSeq bool) ([]folder.Entry, error) {
	defer trace.StartRegion(ctx, "maddy-storage/folder.repository.sqlite.DeleteEntryByRange").End()

	var deleted []folder.Entry

	err := r.inTx(ctx, false, func(tx repo) error {
		entries, err := tx.GetEntryByRange(ctx, folderID, ranges, returnSeq)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			return nil
		}

		uids := make([]j.Expression, len(entries))
		for i, e := range entries {
			uids[i] = j.Int(int64(e.IMAPUID))
		}

		now := time.Now()

		var rows []model.FolderEntries
		stmt := table.FolderEntries.UPDATE().
			SET(table.FolderEntries.DeletedAt.SET(sqlite.Timestamp(now))).
			WHERE(table.FolderEntries.FolderID.EQ(sqlite.ULID(folderID)).
				AND(table.FolderEntries.UID.IN(uids...)).
				AND(table.FolderEntries.DeletedAt.IS_NULL())).
			RETURNING(table.FolderEntries.UID)
		if err := tx.executor.Query(ctx, stmt, &rows); err != nil {
			return err
		}

		updated := make(map[uint32]struct{}, len(rows))
		for _, row := range rows {
			updated[uint32(row.UID)] = struct{}{}
		}
		for _, e := range entries {
			if _, ok := updated[e.IMAPUID]; ok {
				e.DeletedAt = now
				deleted = append(deleted, e)
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	if deleted == nil {
		deleted = []folder.Entry{}
	}
	return deleted, nil
}

func (r repo) UsedFlags(ctx context.Context, folderID ulid.ULID) ([]string, error) {
	var rows []struct{ Flag string }

	stmt := j.SELECT(table.MessageFlags.Flag.AS("flag")).
		FROM(table.FolderEntries.INNER_JOIN(table.MessageFlags,
			table.FolderEntries.MessageID.EQ(table.MessageFlags.MessageID))).
		WHERE(table.FolderEntries.FolderID.EQ(sqlite.ULID(folderID)).
			AND(table.FolderEntries.DeletedAt.IS_NULL()))
	if err := r.executor.Query(ctx, stmt, &rows); err != nil {
		return nil, fmt.Errorf("failed to fetch used flags for folder %v: %w", folderID, err)
	}

	flags := make([]string, len(rows))
	for i, row := range rows {
		flags[i] = row.Flag
	}
	return flags, nil
}

func (r repo) DeletedEntries(ctx context.Context, folderID ulid.ULID, deletedLt time.Time, limit int) ([]folder.Entry, error) {
	var rows []entryRow
	stmt := j.SELECT(table.FolderEntries.AllColumns).
		FROM(table.FolderEntries).
		WHERE(table.FolderEntries.FolderID.EQ(sqlite.ULID(folderID)).
			AND(table.FolderEntries.DeletedAt.LT(sqlite.Timestamp(deletedLt)))).
		ORDER_BY(table.FolderEntries.DeletedAt).
		LIMIT(int64(limit))
	if err := r.executor.Query(ctx, stmt, &rows); err != nil {
		return nil, err
	}

	return entriesAsModels(rows)
}

func (r repo) TouchEntries(ctx context.Context, folderID ulid.ULID, ids []ulid.ULID, modSeq folder.ModSeq) (map[ulid.ULID]folder.ModSeq, error) {
	resMap := make(map[ulid.ULID]folder.ModSeq, len(ids))
	if len(ids) == 0 {
		return resMap, nil
	}

	var rows []model.FolderEntries
	stmt := table.FolderEntries.UPDATE().
		SET(table.FolderEntries.Modseq.SET(
			j.IntExp(j.Func("max", table.FolderEntries.Modseq, j.Int(int64(modSeq)))),
		)).
		WHERE(table.FolderEntries.FolderID.EQ(sqlite.ULID(folderID)).
			AND(table.FolderEntries.MessageID.IN(sqlite.ULIDList(ids)...))).
		RETURNING(table.FolderEntries.MessageID, table.FolderEntries.Modseq)
	if err := r.executor.Query(ctx, stmt, &rows); err != nil {
		return nil, storeerrors.InternalError{Reason: err}
	}

	for _, row := range rows {
		if row.MessageID == nil {
			continue
		}
		id, err := ulidFromBlob(*row.MessageID)
		if err != nil {
			return nil, storeerrors.InternalError{Reason: err}
		}
		resMap[id] = folder.ModSeq(row.Modseq)
	}
	return resMap, nil
}

func (r repo) Tx(ctx context.Context, readOnly bool, f func(r folder.Repo) error) error {
	return r.inTx(ctx, readOnly, func(tx repo) error {
		return f(tx)
	})
}
