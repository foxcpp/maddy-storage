package foldersqlite

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"runtime/trace"
	"sort"
	"strings"

	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/pkg/storeerrors"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite"
	"github.com/mattn/go-sqlite3"
	"github.com/oklog/ulid/v2"
	"gorm.io/gorm"
)

type repo struct {
	db sqlite.DB
}

func New(db sqlite.DB) folder.Repo {
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

func (r repo) GetByID(ctx context.Context, id ulid.ULID) (*folder.Folder, error) {
	defer trace.StartRegion(ctx, "folder.Repository.GetByID").End()

	var f folderDTO

	err := r.db.Gorm(ctx).
		Model(&folderDTO{}).
		Where("folders.id = ?", id).
		Limit(1).
		Find(&f).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, folder.ErrNotFound
		}
		return nil, storeerrors.InternalError{Reason: err}
	}

	return asModel(&f), nil
}

func (r repo) GetByPath(ctx context.Context, accountID ulid.ULID, path string) (*folder.Folder, error) {
	defer trace.StartRegion(ctx, "folder.Repository.GetByPath").End()

	var f folderDTO

	err := r.db.Gorm(ctx).
		Model(&folderDTO{}).
		Where("folders.account_id = ?", accountID).
		Where("folders.path = ?", path).
		Limit(1).
		Find(&f).Error
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
		for _, f := range foldersFiltered {
			if regex.MatchString(f.Path) {
				foldersFiltered = append(foldersFiltered, f)
			}
		}
		return foldersFiltered, nil
	}

	return reDTO, nil
}

func (r repo) GetByAccount(ctx context.Context, accountID ulid.ULID, f folder.Filter, order folder.Order) ([]folder.Folder, error) {
	defer trace.StartRegion(ctx, "folder.Repository.GetByAccount").End()

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
	defer trace.StartRegion(ctx, "folder.Repository.GetByAccount").End()

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
	defer trace.StartRegion(ctx, "folder.Repository.GetByPrefix").End()

	var dtoMap map[ulid.ULID]folderDTO

	err := r.db.Gorm(ctx).Transaction(func(tx *gorm.DB) error {
		for _, p := range prefixes {
			var dtos []folderDTO

			err := addFilterToQuery(r.db.Gorm(ctx).
				Model(&folderDTO{}).
				Where("folders.account_id = ?", accountID).
				Where("folders.path = ? OR folders.path LIKE ?", p, p+folder.PathSeparator+"%"),
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
		return models[i].CreatedAt_.Before(models[j].CreatedAt_)
	})

	return models, nil
}

func (r repo) Create(ctx context.Context, f *folder.Folder) error {
	defer trace.StartRegion(ctx, "folder.Repository.Create").End()

	dto := asDTO(f)

	err := r.db.Gorm(ctx).Create(dto).Error
	if err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return folder.ErrAlreadyExists
		}
		var sqlErr sqlite3.Error
		if errors.As(err, &sqlErr) && errors.Is(sqlErr.ExtendedCode, sqlite3.ErrConstraintUnique) {
			return folder.ErrAlreadyExists
		}
		// TODO: Foreign key constraints, etc.
		return storeerrors.InternalError{Reason: err}
	}

	return nil
}

func (r repo) Update(ctx context.Context, f *folder.Folder) error {
	defer trace.StartRegion(ctx, "folder.Repository.Update").End()

	dto := asDTO(f)

	q := r.db.Gorm(ctx).
		Model(dto).
		Where("folders.id = ? AND folders.updated_at = ?", dto.ID, f.InitialUpdatedAt).
		Updates(map[string]interface{}{
			"subscribed":  dto.Subscribed,
			"special_use": dto.Role,
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
	defer trace.StartRegion(ctx, "folder.Repository.Delete").End()

	err := r.db.Gorm(ctx).
		Where("folders.id = ?", folderID).
		Delete(&folderDTO{}).Error
	if err != nil {
		// TODO: Foreign key constraints, etc.
		return storeerrors.InternalError{Reason: err}
	}

	return nil
}

func (r repo) RenameTree(ctx context.Context, accountID ulid.ULID, path, newPath string) ([]folder.RenamedFolder, error) {
	defer trace.StartRegion(ctx, "folder.Repository.RenameTree").End()

	var data []struct {
		ID      [16]byte `gorm:"id"`
		NewPath string   `gorm:"new_path"`
	}

	err := r.db.Gorm(ctx).
		Raw(`
			UPDATE folders SET path = ? || substr(path, ?)
			WHERE folders.account_id = ? AND folders.path = ? OR folders.path LIKE ?
			RETURNING folders.id AS id, folders.path AS new_name`,
			newPath, len(path)+1, accountID, path, path+folder.PathSeparator+"%").
		Find(&data).Error
	if err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, folder.ErrAlreadyExists
		}
		var sqlErr sqlite3.Error
		if errors.As(err, &sqlErr) && errors.Is(sqlErr.ExtendedCode, sqlite3.ErrConstraintUnique) {
			return nil, folder.ErrAlreadyExists
		}
		return nil, storeerrors.InternalError{Reason: err}
	}

	res := make([]folder.RenamedFolder, len(data))
	for i, f := range data {
		res[i] = folder.RenamedFolder{
			ID:      f.ID,
			OldPath: strings.Replace(f.NewPath, newPath, path, 1),
			NewPath: f.NewPath,
		}
	}

	return res, err
}

func (r repo) DeleteTree(ctx context.Context, accountID ulid.ULID, root string) ([]folder.DeletedFolder, error) {
	defer trace.StartRegion(ctx, "folder.Repository.DeleteTree").End()

	var data []struct {
		ID   [16]byte
		Path string
	}

	err := r.db.Gorm(ctx).
		Raw(`
			DELETE FROM folders
			WHERE folders.account_id = ? AND folders.path = ? OR folders.path LIKE ?
			RETURNING folders.id AS id, folders.path AS path`,
			accountID, root, root+folder.PathSeparator+"%").
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

func (r repo) NextUID(ctx context.Context, folderID ulid.ULID, n int) ([]uint32, error) {
	defer trace.StartRegion(ctx, "folder.Repository.NextUID").End()

	if n <= 0 {
		panic("n must be positive")
	}

	// For SQLite, we store uidnext variable in the folder value.
	var lastUID uint32

	err := r.db.Gorm(ctx).Raw(`
		UPDATE folders 
		SET uidnext = uidnext + ?
		WHERE folders.id = ?
		RETURNING uidnext - 1`, n, folderID).Scan(&lastUID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, folder.ErrNotFound
		}
		return nil, storeerrors.InternalError{Reason: err}
	}

	uids := make([]uint32, n)
	for i := lastUID - uint32(n) + 1; i <= lastUID; i++ {
		uids[i] = i
	}

	return uids, nil
}

func (r repo) CreateEntry(ctx context.Context, entry ...folder.Entry) error {
	defer trace.StartRegion(ctx, "folder.Repository.CreateEntry").End()

	dtos := make([]entryDTO, len(entry))
	for i, ent := range entry {
		dtos[i] = *entryAsDTO(&ent)
	}

	err := r.db.Gorm(ctx).Create(dtos).Error
	if err != nil {
		var sqlErr sqlite3.Error
		if errors.As(err, &sqlErr) && errors.Is(sqlErr.ExtendedCode, sqlite3.ErrConstraintForeignKey) {
			return folder.ErrDanglingEntry
		}

		// TODO: Foreign key constraints, etc.
		return storeerrors.InternalError{Reason: err}
	}

	return nil
}

func (r repo) ReplaceEntries(ctx context.Context, old []folder.Entry, new []folder.Entry) error {
	defer trace.StartRegion(ctx, "folder.Repository.ReplaceEntries").End()

	err := r.db.Gorm(ctx).Transaction(func(tx *gorm.DB) error {
		for _, ent := range old {
			err := r.db.Gorm(ctx).
				Where("folder_entries.folder_id = ?", ent.FolderID_).
				Where("folder_entries.msg_id = ?", ent.MsgID_).
				Delete(&entryDTO{}).Error
			if err != nil {
				return err
			}
		}
		for _, ent := range new {
			err := r.db.Gorm(ctx).Create(entryAsDTO(&ent)).Error
			if err != nil {
				return err
			}
		}

		return nil
	})
	return err
}

func (r repo) GetEntryByUIDRange(ctx context.Context, folderID ulid.ULID, ranges ...folder.UIDRange) ([]folder.Entry, error) {
	defer trace.StartRegion(ctx, "folder.Repository.GetEntryByUIDRange").End()

	if len(ranges) == 1 {
		var entries []entryDTO

		err := r.db.Gorm(ctx).
			Model(&entries).
			Where("folder_entries.folder_id = ?", folderID).
			Where("folder_entries.uid BETWEEN ? AND ?", ranges[0].Since, ranges[0].Until).
			Find(&entries).Error
		if err != nil {
			return nil, err
		}

		models := make([]folder.Entry, 0, len(entries))
		for _, ent := range entries {
			models = append(models, *entryAsModel(&ent))
		}
		return models, nil
	} else {
		entryMap := make(map[uint32]entryDTO)

		err := r.db.Gorm(ctx).Transaction(func(tx *gorm.DB) error {
			for _, r := range ranges {
				var entries []entryDTO

				err := tx.Model(&entries).
					Where("folder_entries.folder_id = ?", folderID).
					Where("folder_entries.uid BETWEEN ? AND ?", r.Since, r.Until).
					Find(&entries).Error
				if err != nil {
					return err
				}

				for _, ent := range entries {
					entryMap[ent.UID] = ent
				}
			}
			return nil
		}, &sql.TxOptions{
			Isolation: sql.LevelRepeatableRead,
			ReadOnly:  true,
		})
		if err != nil {
			return nil, err
		}

		models := make([]folder.Entry, 0, len(entryMap))
		for _, ent := range entryMap {
			models = append(models, *entryAsModel(&ent))
		}

		return models, nil
	}
}

func (r repo) CountEntryByUIDRange(ctx context.Context, folderID ulid.ULID, ranges ...folder.UIDRange) (int, error) {
	defer trace.StartRegion(ctx, "folder.Repository.CountEntryByUIDRange").End()

	if len(ranges) == 1 {
		var cnt int64
		err := r.db.Gorm(ctx).
			Model(&entryDTO{}).
			Where("folder_entries.folder_id = ?", folderID).
			Where("folder_entries.uid BETWEEN ? AND ?", ranges[0].Since, ranges[0].Until).
			Count(&cnt).Error
		return int(cnt), err
	} else {
		var entryMap map[uint32]bool
		err := r.db.Gorm(ctx).Transaction(func(tx *gorm.DB) error {
			for _, r := range ranges {
				var entries []entryDTO

				err := tx.Model(&entries).
					Where("folder_entries.folder_id = ?", folderID).
					Where("folder_entries.uid BETWEEN ? AND ?", r.Since, r.Until).
					Find(&entries).Error
				if err != nil {
					return err
				}

				for _, ent := range entries {
					entryMap[ent.UID] = true
				}
			}
			return nil
		}, &sql.TxOptions{
			Isolation: sql.LevelRepeatableRead,
			ReadOnly:  true,
		})
		if err != nil {
			return 0, err
		}
		return len(entryMap), nil
	}
}

func (r repo) DeleteEntryByUIDRange(ctx context.Context, folderID ulid.ULID, ranges ...folder.UIDRange) error {
	defer trace.StartRegion(ctx, "folder.Repository.DeleteEntryByUIDRange").End()

	if len(ranges) == 1 {
		err := r.db.Gorm(ctx).
			Where("folder_entries.folder_id = ?", folderID).
			Where("folder_entries.uid BETWEEN ? AND ?", ranges[0].Since, ranges[0].Until).
			Delete(&entryDTO{}).Error
		if err != nil {
			return err
		}
		return nil
	} else {
		err := r.db.Gorm(ctx).Transaction(func(tx *gorm.DB) error {
			for _, r := range ranges {
				err := tx.
					Where("folder_entries.folder_id = ?", folderID).
					Where("folder_entries.uid BETWEEN ? AND ?", r.Since, r.Until).
					Delete(&entryDTO{}).Error
				if err != nil {
					return err
				}
			}
			return nil
		}, &sql.TxOptions{
			Isolation: sql.LevelRepeatableRead,
		})
		if err != nil {
			return err
		}

		return nil
	}
}

func (r repo) Tx(ctx context.Context, readOnly bool, f func(r folder.Repo) error) error {
	return r.db.Tx(ctx, readOnly, func(tx sqlite.DB) error {
		txRepo := repo{db: tx}
		return f(txRepo)
	})
}
