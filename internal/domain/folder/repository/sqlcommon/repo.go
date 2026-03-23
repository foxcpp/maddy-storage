package foldersql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"runtime/trace"
	"sort"
	"strings"
	"time"

	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/pkg/storeerrors"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlcommon"
	"github.com/oklog/ulid/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type repo struct {
	db sqlcommon.DB
}

func New(db sqlcommon.DB) folder.Repo {
	return repo{db: db}
}

var likeEscape = strings.NewReplacer(`\`, `\\`, `%`, `\%`)

func addFilterToQuery(q *gorm.DB, f *folder.Filter) *gorm.DB {
	// regexp is handled in GetByAccount

	if f.NameContains != nil {
		q = q.Where(`folders.name LIKE ? ESCAPE '\'`, "%"+likeEscape.Replace(*f.NameContains)+"%")
	}
	if f.Path != nil {
		q = q.Where("folders.path = ?", *f.Path)
	}
	if f.PathPrefix != nil {
		q = q.Where(`folders.path LIKE ? ESCAPE '\'`, likeEscape.Replace(*f.PathPrefix)+"%")
	}
	if f.ParentID != nil {
		q = q.Where("folders.parent_id = ?", *f.ParentID)
	}
	if f.ParentPath != nil {
		q = q.Where(`folders.path LIKE ? ESCAPE '\'`, likeEscape.Replace(*f.ParentPath)+folder.PathSeparator+"%")
	}
	if f.Subscribed != nil {
		subNum := 0
		if *f.Subscribed {
			subNum = 1
		}
		q = q.Where("folders.subscribed = ?", subNum)
	}
	if f.HasRole != nil {
		if *f.HasRole {
			q = q.Where("folders.role IS NOT NONE")
		} else {
			q = q.Where("folders.role IS NONE")
		}
	}
	if f.Role != nil {
		q = q.Where("folders.role = ?", *f.Role)
	}

	return q
}

func orderKey(o folder.Order) string {
	switch o {
	case folder.OrderBySortOrder:
		return "folders.sort_order, folders.name"
	case folder.OrderBySortOrderDesc:
		return "folders.sort_order, folders.name DESC"
	case folder.OrderByCreatedAt:
		return "folders.created_at, folders.name"
	case folder.OrderByCreatedAtDesc:
		return "folders.created_at DESC, folders.name DESC"
	case folder.OrderByName:
		return "folders.name"
	case folder.OrderByNameDesc:
		return "folders.name DESC"
	default:
		panic("unknown sort order")
	}
}

var _ folder.Repo = &repo{}

func (r repo) GetByID(ctx context.Context, id ulid.ULID) (*folder.Folder, error) {
	defer trace.StartRegion(ctx, "maddy-storage/folder.repository.sqlcommon.GetByID").End()

	var f folderDTO

	err := r.db.Gorm(ctx).
		Model(&folderDTO{}).
		Where("folders.id = ?", id).
		First(&f).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, folder.ErrNotFound
		}
		return nil, storeerrors.InternalError{Reason: err}
	}

	return asModel(&f), nil
}

func (r repo) GetByPath(ctx context.Context, accountID ulid.ULID, path string) (*folder.Folder, error) {
	defer trace.StartRegion(ctx, "maddy-storage/folder.repository.sqlcommon.GetByPath").End()

	var f folderDTO

	err := r.db.Gorm(ctx).
		Model(&folderDTO{}).
		Where("folders.account_id = ?", accountID).
		Where("folders.path = ?", path).
		First(&f).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, folder.ErrNotFound
		}
		return nil, storeerrors.InternalError{Reason: err}
	}

	return asModel(&f), nil
}

func (r repo) getByRegexp(tx *gorm.DB, accountID ulid.ULID, f folder.Filter, order folder.Order, regex *regexp.Regexp) ([]folderDTO, error) {
	prefix, complete := regex.LiteralPrefix()

	var reDTO []folderDTO
	err := addFilterToQuery(tx.
		Model(&folderDTO{}).
		Where("folders.account_id = ?", accountID).
		Where(`folders.path LIKE ? ESCAPE '\'`, likeEscape.Replace(prefix)+"%"),
		&f).
		Order(orderKey(order)).
		Find(&reDTO).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, folder.ErrNotFound
		}
		return nil, storeerrors.InternalError{Reason: err}
	}
	if !complete {
		foldersFiltered := reDTO[:0]
		for _, f := range reDTO {
			if regex.MatchString(f.Path) {
				foldersFiltered = append(foldersFiltered, f)
			}
		}
		return foldersFiltered, nil
	}

	return reDTO, nil
}

func (r repo) GetByAccount(ctx context.Context, accountID ulid.ULID, f folder.Filter, order folder.Order) ([]folder.Folder, error) {
	defer trace.StartRegion(ctx, "maddy-storage/folder.repository.sqlcommon.GetByAccount").End()

	var (
		dto []folderDTO
		err error
	)
	switch len(f.PathRegex) {
	case 0:
		err = addFilterToQuery(r.db.Gorm(ctx).
			Model(&folderDTO{}).
			Where("folders.account_id = ?", accountID),
			&f).
			Order(orderKey(order)).
			Find(&dto).Error
	case 1:
		dto, err = r.getByRegexp(r.db.Gorm(ctx), accountID, f, order, f.PathRegex[0])
	default:
		err = r.db.Gorm(ctx).Transaction(func(tx *gorm.DB) error {
			for _, re := range f.PathRegex {
				reDTO, err := r.getByRegexp(tx, accountID, f, order, re)
				if err != nil {
					return err
				}
				dto = append(dto, reDTO...)
			}
			return nil
		}, &sql.TxOptions{
			ReadOnly: true,
		})
	}
	if err != nil {
		return nil, storeerrors.InternalError{Reason: err}
	}

	models := make([]folder.Folder, len(dto))
	for i, d := range dto {
		models[i] = *asModel(&d)
	}

	return models, nil
}

func (r repo) CountByAccount(ctx context.Context, accountID ulid.ULID, f folder.Filter) (int, error) {
	defer trace.StartRegion(ctx, "maddy-storage/folder.repository.sqlcommon.GetByAccount").End()

	var (
		cnt int64
		err error
	)
	switch len(f.PathRegex) {
	case 0:
		err = addFilterToQuery(r.db.Gorm(ctx).
			Model(&folderDTO{}).
			Where("folders.account_id = ?", accountID),
			&f).
			Count(&cnt).Error
	case 1:
		dto, err := r.getByRegexp(r.db.Gorm(ctx), accountID, f, folder.OrderByCreatedAt, f.PathRegex[0])
		if err != nil {
			return 0, err
		}
		cnt = int64(len(dto))
	default:
		ids := make(map[ulid.ULID]struct{})
		err = r.db.Gorm(ctx).Transaction(func(tx *gorm.DB) error {
			for _, re := range f.PathRegex {
				reDTO, err := r.getByRegexp(tx, accountID, f, folder.OrderByCreatedAt, re)
				if err != nil {
					return err
				}
				for _, f := range reDTO {
					ids[f.ID] = struct{}{}
				}
			}
			return nil
		}, &sql.TxOptions{
			ReadOnly: true,
		})
		cnt = int64(len(ids))
	}
	if err != nil {
		return 0, storeerrors.InternalError{Reason: err}
	}

	return int(cnt), nil
}

func (r repo) GetByPrefix(ctx context.Context, accountID ulid.ULID, f folder.Filter, prefixes ...string) ([]folder.Folder, error) {
	defer trace.StartRegion(ctx, "maddy-storage/folder.repository.sqlcommon.GetByPrefix").End()

	var dtoMap map[ulid.ULID]folderDTO

	err := r.db.Gorm(ctx).Transaction(func(tx *gorm.DB) error {
		for _, p := range prefixes {
			var dtos []folderDTO

			err := addFilterToQuery(r.db.Gorm(ctx).
				Model(&folderDTO{}).
				Where("folders.account_id = ?", accountID).
				Where(`folders.path = ? OR folders.path LIKE ? ESCAPE '\'`, p, likeEscape.Replace(p)+folder.PathSeparator+"%"),
				&f).
				Find(&dtos).Error
			if err != nil {
				return err
			}

			for _, dto := range dtos {
				dtoMap[dto.ID] = dto
			}
		}
		return nil
	}, &sql.TxOptions{
		Isolation: sql.LevelRepeatableRead,
		ReadOnly:  true,
	})
	if err != nil {
		return nil, storeerrors.InternalError{Reason: err}
	}

	models := make([]folder.Folder, 0, len(dtoMap))
	for _, dto := range dtoMap {
		models = append(models, *asModel(&dto))
	}

	sort.Slice(models, func(i, j int) bool {
		return models[i].CreatedAt.Before(models[j].CreatedAt)
	})

	return models, nil
}

func (r repo) Create(ctx context.Context, f *folder.Folder) error {
	defer trace.StartRegion(ctx, "maddy-storage/folder.repository.sqlcommon.Create").End()

	dto := asDTO(f)

	err := r.db.Gorm(ctx).Create(dto).Error
	if err != nil {
		if r.db.IsUniqueConstraintError(err) {
			return folder.ErrAlreadyExists
		}
		if r.db.IsForeignConstraintError(err) {
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
	defer trace.StartRegion(ctx, "maddy-storage/folder.repository.sqlcommon.Update").End()

	dto := asDTO(f)

	q := r.db.Gorm(ctx).
		Model(dto).
		Where("folders.id = ? AND folders.updated_at = ?", dto.ID, f.InitialUpdatedAt).
		Updates(map[string]interface{}{
			"subscribed": dto.Subscribed,
			"role":       dto.Role,
		})
	if err := q.Error; err != nil {
		return err
	}
	if q.RowsAffected == 0 {
		return folder.ErrNotFound // XXX: Figure out a way to differentiate OCC failure from missing object
	}

	return nil
}

func (r repo) Delete(ctx context.Context, folderID ulid.ULID) error {
	defer trace.StartRegion(ctx, "maddy-storage/folder.repository.sqlcommon.Delete").End()

	err := r.db.Gorm(ctx).
		Where("folders.id = ?", folderID).
		Delete(&folderDTO{}).Error
	if err != nil {
		if r.db.IsForeignConstraintError(err) {
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
	defer trace.StartRegion(ctx, "maddy-storage/folder.repository.sqlcommon.RenameMove").End()

	var data []struct {
		ID      ulid.ULID `gorm:"id"`
		NewPath string    `gorm:"new_path"`
	}

	var oldParentID, newParentID *ulid.ULID
	var oldPath, newPath string
	if oldParent != nil {
		oldParentID = &oldParent.ID
		oldPath = oldParent.Path + folder.PathSeparator + oldName
	} else {
		oldPath = oldName
	}
	if newParent != nil {
		newParentID = &newParent.ID
		newPath = newParent.Path + folder.PathSeparator + newName
	} else {
		newPath = newName
	}

	err := r.db.Gorm(ctx).Transaction(func(tx *gorm.DB) error {
		var dataFirst []struct {
			ID      ulid.ULID `gorm:"id"`
			NewPath string    `gorm:"new_path"`
		}

		// 1. Change parent, update path and name.
		res1 := tx.
			Raw(`
				UPDATE folders 
				SET
					parent_id = ?,
					path = ?,
					name = ?
				WHERE
					parent_id IS ? -- Constraints are redundant for consistency.
					AND path = ?
					AND name = ?
				RETURNING folders.id AS id, folders.path AS new_path
				`,
				newParentID, newPath, newName,
				oldParentID, oldPath, oldName).
			Find(&dataFirst)
		if err := res1.Error; err != nil {
			if r.db.IsUniqueConstraintError(err) {
				return folder.ErrAlreadyExists
			}
			if r.db.IsForeignConstraintError(err) {
				return storeerrors.NotExistsError{Text: "parent folder does not exist"}
			}
			return storeerrors.InternalError{Reason: err}
		}
		if res1.RowsAffected == 0 {
			return folder.ErrNotFound
		}

		// 2. Update path for children directories (parent_id stays the same).
		err := tx.
			Raw(`
			UPDATE folders SET path = ? || substr(path, ?)
			WHERE folders.account_id = ? AND folders.path LIKE ? ESCAPE '\'
			RETURNING folders.id AS id, folders.path AS new_path`,
				newPath, len(oldPath)+1, accountID, likeEscape.Replace(oldPath)+folder.PathSeparator+"%").
			Find(&data).Error
		if err != nil {
			if r.db.IsUniqueConstraintError(err) { // Pretty much should be impossible, but check just in case.
				return folder.ErrAlreadyExists
			}
			return storeerrors.InternalError{Reason: err}
		}

		data = append(data, dataFirst...)
		return nil
	})

	res := make([]folder.RenamedFolder, len(data))
	for i, f := range data {
		res[i] = folder.RenamedFolder{
			ID:      f.ID,
			OldPath: strings.Replace(f.NewPath, newPath, oldPath, 1),
			NewPath: f.NewPath,
		}
	}

	return res, err
}

func (r repo) DeleteTree(ctx context.Context, accountID ulid.ULID, root string) ([]folder.DeletedFolder, error) {
	defer trace.StartRegion(ctx, "maddy-storage/folder.repository.sqlcommon.DeleteTree").End()

	var data []struct {
		ID   ulid.ULID
		Path string
	}

	err := r.db.Gorm(ctx).
		Raw(`
			DELETE FROM folders
			WHERE folders.account_id = ? AND folders.path = ? OR folders.path LIKE ? ESCAPE '\'
			RETURNING folders.id AS id, folders.path AS path`,
			accountID, root, likeEscape.Replace(root)+folder.PathSeparator+"%").
		Find(&data).Error
	if err != nil {
		return nil, storeerrors.InternalError{Reason: err}
	}

	ids := make([]folder.DeletedFolder, len(data))
	for i, id := range data {
		ids[i] = folder.DeletedFolder{
			ID:   id.ID,
			Path: id.Path,
		}
	}

	return ids, err
}

func addAtConds(q *gorm.DB, at, deletesAt folder.ModSeq) *gorm.DB {
	// XXX: Duplicated in searcher code.

	return q.Where("folder_entries.created_at_modseq <= ?").
		Where("(folder_entries.deleted_at IS NULL OR folder_entries.deleted_at > ?)", at, deletesAt)
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

func (r repo) addRangeConds(ctx context.Context, q *gorm.DB, rang folder.Range) *gorm.DB {
	or := r.db.Gorm(ctx)
	if rang.Values != nil {
		if rang.SeqNum {
			or = or.Or("seqnums.seq IN (?)", rang.Values)
		} else {
			or = or.Or("folder_entries.uid IN (?)", rang.Values)
		}
	}
	for _, inter := range rang.Intervals {
		if rang.SeqNum {
			or = or.Or("seqnums.seq BETWEEN ? AND ?", inter.Since, inter.Until)
		} else {
			or = or.Or("folder_entries.uid BETWEEN ? AND ?", inter.Since, inter.Until)
		}
	}
	return q.Where(or)
}

func (r repo) CreateEntry(ctx context.Context, entry ...folder.Entry) error {
	defer trace.StartRegion(ctx, "maddy-storage/folder.repository.sqlcommon.CreateEntry").End()

	dtos := make([]entryDTO, len(entry))
	for i, ent := range entry {
		dtos[i] = *entryAsDTO(&ent)
	}

	err := r.db.Gorm(ctx).Create(dtos).Error
	if err != nil {
		if r.db.IsForeignConstraintError(err) {
			return folder.ErrDanglingEntry
		}
		return storeerrors.InternalError{Reason: err}
	}

	return nil
}

func (r repo) ReplaceEntries(ctx context.Context, old []folder.Entry, new []folder.Entry, modSeq folder.ModSeq) error {
	defer trace.StartRegion(ctx, "maddy-storage/folder.repository.sqlcommon.ReplaceEntries").End()

	err := r.db.Gorm(ctx).Transaction(func(tx *gorm.DB) error {
		for _, ent := range old {
			err := tx.Table("folder_entries").
				Where("folder_entries.folder_id = ?", ent.FolderID).
				Where("folder_entries.message_id = ?", ent.MsgID).
				Where("folder_entries.deleted_at IS NULL").
				Updates(map[string]interface{}{"deleted_at": time.Now(), "modseq": modSeq}).Error
			if err != nil {
				return err
			}
		}
		for _, ent := range new {
			err := tx.Create(entryAsDTO(&ent)).Error
			if err != nil {
				return err
			}
		}

		return nil
	})
	return err
}

func (r repo) GetEntryByRange(ctx context.Context, folderID ulid.ULID, ranges folder.Range, returnSeq bool) ([]folder.Entry, error) {
	defer trace.StartRegion(ctx, "maddy-storage/folder.repository.sqlcommon.GetEntryByRange").End()

	var entries []entryDTO

	q := r.db.Gorm(ctx).
		Table("folder_entries").
		Where("folder_entries.folder_id = ?", folderID)
	if returnSeq {
		q = q.Select("folder_entries.*, seqnums.seq AS seq")
	} else {
		q = q.Select("folder_entries.*")
	}

	if ranges.SeqNum || returnSeq {
		q = joinSeqNum(q, ranges.At, ranges.DeletesAt, folderID)
	} else if ranges.At != 0 {
		q = addAtConds(q, ranges.At, ranges.DeletesAt)
	} else {
		q = q.Where("folder_entries.deleted_at IS NULL")
	}
	q = r.addRangeConds(ctx, q, ranges)

	err := q.Find(&entries).Error
	if err != nil {
		return nil, fmt.Errorf("find: %w", err)
	}

	models := make([]folder.Entry, 0, len(entries))
	for _, ent := range entries {
		models = append(models, *entryAsModel(&ent))
	}
	return models, nil
}

func (r repo) CountEntryByRange(ctx context.Context, folderID ulid.ULID, ranges folder.Range) (int, error) {
	defer trace.StartRegion(ctx, "maddy-storage/folder.repository.sqlcommon.CountEntryByRange").End()

	var cnt int64
	q := r.db.Gorm(ctx).
		Table("folder_entries").
		Where("folder_entries.folder_id = ?", folderID)

	if ranges.SeqNum {
		q = joinSeqNum(q, ranges.At, ranges.DeletesAt, folderID)
	} else if ranges.At != 0 {
		q = addAtConds(q, ranges.At, ranges.DeletesAt)
	} else {
		q = q.Where("folder_entries.deleted_at IS NULL")
	}
	q = r.addRangeConds(ctx, q, ranges)

	err := q.Count(&cnt).Error
	return int(cnt), err
}

func (r repo) DeleteEntryByIDs(ctx context.Context, folderID ulid.ULID, ids []ulid.ULID, modSeq folder.ModSeq) ([]folder.Entry, error) {
	defer trace.StartRegion(ctx, "maddy-storage/folder.repository.sqlcommon.DeleteEntryByIDs").End()

	now := time.Now()
	var entries []entryDTO

	err := r.db.Gorm(ctx).Raw(`
		UPDATE folder_entries
		SET deleted_at = ?, modseq = ?
		WHERE folder_id = ? AND message_id IN (?) AND deleted_at IS NULL
		RETURNING *`,
		now, modSeq, folderID, ids,
	).Find(&entries).Error
	if err != nil {
		return nil, err
	}

	models := make([]folder.Entry, 0, len(entries))
	for _, ent := range entries {
		models = append(models, *entryAsModel(&ent))
	}
	return models, nil
}

func (r repo) DeleteEntryByRange(ctx context.Context, folderID ulid.ULID, ranges folder.Range, returnSeq bool) ([]folder.Entry, error) {
	defer trace.StartRegion(ctx, "maddy-storage/folder.repository.sqlcommon.DeleteEntryByRange").End()

	now := time.Now()
	var entries []entryDTO

	q := r.db.Gorm(ctx).Table("folder_entries").
		Clauses(clause.Returning{}).
		Where("folder_entries.folder_id = ?", folderID)

	if ranges.SeqNum || returnSeq {
		q = joinSeqNum(q, ranges.At, ranges.DeletesAt, folderID)
	} else if ranges.At != 0 {
		q = addAtConds(q, ranges.At, ranges.DeletesAt)
	} else {
		q = q.Where("folder_entries.deleted_at IS NULL")
	}
	q = r.addRangeConds(ctx, q, ranges)

	err := q.Update("deleted_at", now).Error
	if err != nil {
		return nil, err
	}

	models := make([]folder.Entry, 0, len(entries))
	for _, ent := range entries {
		models = append(models, *entryAsModel(&ent))
	}
	return models, nil
}

func (r repo) UsedFlags(ctx context.Context, folderID ulid.ULID) ([]string, error) {
	var flags []string
	err := r.db.Gorm(ctx).
		Select("message_flags.flag").
		Table("folder_entries").
		Joins("JOIN message_flags ON folder_entries.message_id = message_flags.message_id").
		Where("folder_entries.folder_id = ?", folderID).
		Where("folder_entries.deleted_at IS NULL").
		Pluck("message_flags.flag", &flags).Error
	if err != nil {
		return nil, fmt.Errorf("failed to fetch used flags for folder %v: %w", folderID, err)
	}
	return flags, nil
}

func (r repo) DeletedEntries(ctx context.Context, folderID ulid.ULID, deletedLt time.Time, limit int) ([]folder.Entry, error) {
	var entries []entryDTO
	err := r.db.Gorm(ctx).Table("folder_entries").
		Where("folder_entries.folder_id = ?", folderID).
		Where("folder_entries.deleted_at < ?", deletedLt).
		Order("folder_entries.deleted_at").
		Limit(limit).
		Find(&entries).Error
	if err != nil {
		return nil, err
	}

	models := make([]folder.Entry, 0, len(entries))
	for _, ent := range entries {
		models = append(models, *entryAsModel(&ent))
	}
	return models, nil
}

func (r repo) TouchEntries(ctx context.Context, folderID ulid.ULID, ids []ulid.ULID, modSeq folder.ModSeq) (map[ulid.ULID]folder.ModSeq, error) {
	resMap := make(map[ulid.ULID]folder.ModSeq, len(ids))
	rows, err := r.db.Gorm(ctx).Raw(`
		UPDATE folder_entries
		SET modseq = max(modseq, ?)
		WHERE folder_id = ? 
		AND message_id IN (?)
		RETURNING message_id, modseq`,
		modSeq, folderID, ids).Rows()
	if err != nil {
		return nil, storeerrors.InternalError{Reason: err}
	}
	defer rows.Close()
	for rows.Next() {
		var id ulid.ULID
		var modSeq uint64
		if err := rows.Scan(&id, &modSeq); err != nil {
			return nil, storeerrors.InternalError{Reason: err}
		}
		resMap[id] = folder.ModSeq(modSeq)
	}
	return resMap, nil
}

func (r repo) Tx(ctx context.Context, readOnly bool, f func(r folder.Repo) error) error {
	return r.db.Tx(ctx, readOnly, func(tx sqlcommon.DB) error {
		txRepo := repo{db: tx}
		return f(txRepo)
	})
}
