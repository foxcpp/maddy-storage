package folderusecase

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/foxcpp/maddy-storage/internal/domain/changelog"
	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/domain/message/searcher"
	"github.com/foxcpp/maddy-storage/internal/pkg/contextlib"
	"github.com/foxcpp/maddy-storage/internal/pkg/storeerrors"
	"github.com/oklog/ulid/v2"
	"go.uber.org/zap"
)

type Folder struct {
	repo      folder.Repo
	imap      folder.IMAPRepo
	changelog changelog.Repo
	searcher  searcher.Searcher
}

func New(repo folder.Repo, imapRepo folder.IMAPRepo, changelog changelog.Repo, searcher searcher.Searcher) Folder {
	return Folder{
		repo:      repo,
		imap:      imapRepo,
		changelog: changelog,
		searcher:  searcher,
	}
}

type ListOpts struct {
	Filter           folder.Filter
	DescendantFilter *folder.Filter

	ReturnNamespace bool

	CheckChildren bool

	CountMsgs       bool
	CountDeleted    bool
	CountUnseen     bool
	CountSize       bool
	ReturnMaxModSeq bool // account modseq
	ReturnIMAPMeta  bool // UIDNEXT, UIDVALIDITY

	SortAsTree bool
}

type FolderData struct {
	Folder             folder.Folder
	IMAP               folder.IMAPFolder
	MatchingDescendant []folder.Folder
	Namespace          folder.Namespace

	HasChildren bool

	Msgs uint32
	Size int64

	DeletedMsgs uint32
	DeletedSize int64

	UnseenMsgs uint32

	MaxModSeq folder.ModSeq
}

func (f Folder) GetByRole(ctx context.Context, accountID ulid.ULID, role folder.Role, fallbackName string) (*folder.Folder, error) {
	roleFolder, err := f.repo.GetByAccount(ctx, accountID, folder.Filter{
		Role: &role,
	}, folder.OrderByName)
	if err != nil || len(roleFolder) == 0 {
		if err != nil && !errors.Is(err, folder.ErrNotFound) {
			return nil, fmt.Errorf("GetByRole %s: %w", role, err)
		}

		fallback, err := f.repo.GetByPath(ctx, accountID, fallbackName)
		if err != nil {
			if errors.Is(err, folder.ErrNotFound) {
				return nil, fmt.Errorf("GetByRole %s: fallback GetByPath %s: %w", role, fallbackName, err)
			}
			return nil, fmt.Errorf("GetByRole %s: %w", role, err)
		}
		return fallback, nil
	}

	return &roleFolder[0], nil
}

func (f Folder) List(ctx context.Context, accountID ulid.ULID, opts *ListOpts, order folder.Order) ([]FolderData, error) {
	foundRoots, err := f.repo.GetByAccount(ctx, accountID, opts.Filter, order)
	if err != nil {
		return nil, err
	}
	if len(foundRoots) == 0 {
		return nil, nil
	}

	var hasChildren map[ulid.ULID]struct{}
	if opts.CheckChildren {
		hasChildren = make(map[ulid.ULID]struct{}, len(foundRoots))
		for _, fold := range foundRoots {
			hasChildren[fold.ParentID] = struct{}{}
		}
	}

	var imapByID map[ulid.ULID]folder.IMAPFolder
	if opts.ReturnIMAPMeta {
		ids := make([]ulid.ULID, len(foundRoots))
		for i, fold := range foundRoots {
			ids[i] = fold.ID
		}
		folders, err := f.imap.GetIMAPFolders(ctx, ids)
		if err != nil {
			return nil, fmt.Errorf("get imap folder: %w", err)
		}
		imapByID = make(map[ulid.ULID]folder.IMAPFolder, len(folders))
		for _, f := range folders {
			imapByID[f.ID] = f
		}
	}

	var folderStats, unseenStats, deletedStats searcher.SearchResult
	if opts.CountMsgs || opts.CountDeleted || opts.CountUnseen || opts.CountSize {
		rootIDs := make([]ulid.ULID, len(foundRoots))
		for i, fold := range foundRoots {
			rootIDs[i] = fold.ID
		}

		var err error
		folderStats, err = f.searcher.Search(ctx, accountID, &searcher.Cond{
			FolderIDs: rootIDs,
		}, searcher.Opts{
			ReturnCount:     opts.CountMsgs,
			ReturnTotalSize: opts.CountSize,
			GroupByFolder:   true,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to fetch folder stats: %w", err)
		}

		if opts.CountUnseen {
			unseenStats, err = f.searcher.Search(ctx, accountID, &searcher.Cond{
				FolderIDs: rootIDs,
				NoFlag:    []string{"\\Seen"},
			}, searcher.Opts{
				ReturnCount:   true,
				GroupByFolder: true,
			})
			if err != nil {
				return nil, fmt.Errorf("failed to fetch unseen stats: %w", err)
			}
		}
		if opts.CountDeleted {
			deletedStats, err = f.searcher.Search(ctx, accountID, &searcher.Cond{
				FolderIDs: rootIDs,
				Flag:      []string{"\\Deleted"},
			}, searcher.Opts{
				ReturnCount:   true,
				GroupByFolder: true,
			})
			if err != nil {
				return nil, fmt.Errorf("failed to fetch unseen stats: %w", err)
			}
		}
	}

	dataList := make([]FolderData, len(foundRoots))
	for i, fold := range foundRoots {
		data := FolderData{
			Folder: fold,
		}

		if opts.DescendantFilter != nil {
			prefix := fold.Path + folder.PathSeparator
			if opts.DescendantFilter.PathPrefix != nil {
				panic("PathPrefix not supported for descendant filtering")
			}
			opts.DescendantFilter.PathPrefix = &prefix
			descendant, err := f.repo.GetByAccount(ctx, accountID, *opts.DescendantFilter, order)
			if err != nil {
				return nil, err
			}
			data.MatchingDescendant = descendant
		}

		if opts.CheckChildren {
			if _, ok := hasChildren[fold.ID]; ok {
				data.HasChildren = true
			} else {
				// TODO: Consider caching here as it is rather expensive to check every time.
				cnt, err := f.repo.CountByAccount(ctx, accountID, folder.Filter{
					ParentID: &fold.ID,
				})
				if err != nil {
					return nil, err
				}
				data.HasChildren = cnt != 0
			}
		}

		if opts.CountMsgs {
			data.Msgs = folderStats.ByFolder[fold.ID].Count
		}
		if opts.CountDeleted {
			data.DeletedMsgs = deletedStats.ByFolder[fold.ID].Count
			data.DeletedSize = deletedStats.ByFolder[fold.ID].TotalSize
		}
		if opts.CountUnseen {
			data.UnseenMsgs = unseenStats.ByFolder[fold.ID].Count
		}
		if opts.CountSize {
			data.Size = folderStats.ByFolder[fold.ID].TotalSize
		}
		if opts.ReturnIMAPMeta {
			var ok bool
			data.IMAP, ok = imapByID[fold.ID]
			if !ok {
				panic("missing imapByID value")
			}
		}

		dataList[i] = data
	}

	if opts.ReturnMaxModSeq {
		modSeq, err := f.imap.LastModSeq(ctx, accountID)
		if err != nil {
			return nil, fmt.Errorf("last modseq: %w", err)
		}
		for i := range dataList {
			dataList[i].MaxModSeq = modSeq
		}
	}

	if opts.SortAsTree {
		folder.SortAsTree(dataList, func(e *FolderData) *folder.Folder {
			return &e.Folder
		}, order.Compare)
	}

	return dataList, nil
}

func (f Folder) Create(ctx context.Context, accountID ulid.ULID, path string, role folder.Role, createParent bool) (*folder.Folder, error) {
	var parent *folder.Folder
	name := path
	if strings.Contains(path, folder.PathSeparator) {
		parentPath := path[:strings.LastIndex(path, folder.PathSeparator)]
		name = path[len(parentPath)+1:]
		var err error
		parent, err = f.repo.GetByPath(ctx, accountID, parentPath)
		if err != nil {
			if !errors.Is(err, folder.ErrNotFound) {
				return nil, err
			}

			if !createParent {
				return nil, storeerrors.NotExistsError{Text: "parent folder does not exist"}
			}

			// TODO: Limit recursion
			parent, err = f.Create(ctx, accountID, parentPath, folder.RoleNone, createParent)
			if err != nil {
				return nil, err
			}
		}
	}

	newFolder, err := folder.NewFolder(parent, accountID, name, role)
	if err != nil {
		return nil, storeerrors.InternalError{Reason: fmt.Errorf("new folder: %w", err)}
	}

	if err := f.repo.Create(ctx, newFolder); err != nil {
		return nil, storeerrors.InternalError{Reason: fmt.Errorf("create: %w", err)}
	}

	if _, err := f.imap.CreateIMAPFolders(ctx, newFolder.ID); err != nil {
		return nil, storeerrors.InternalError{Reason: fmt.Errorf("create imap: %w", err)}
	}

	contextlib.Logger(ctx).Info("created folder", zap.Stringer("id", newFolder.ID), zap.String("path", path))

	return newFolder, nil
}

func (f Folder) Rename(ctx context.Context, accountID ulid.ULID, oldPath, newPath string, createParent bool) ([]folder.RenamedFolder, error) {
	if oldPath == newPath {
		return nil, nil
	}
	if strings.HasPrefix(newPath, oldPath+folder.PathSeparator) {
		return nil, storeerrors.LogicError{Text: "cannot move folder into itself"}
	}

	log := contextlib.Logger(ctx)

	var oldParent *folder.Folder
	oldName := oldPath
	if strings.Contains(oldPath, folder.PathSeparator) {
		parentPath := oldPath[:strings.LastIndex(oldPath, folder.PathSeparator)]
		oldName = oldPath[len(parentPath)+1:]
		var err error
		oldParent, err = f.repo.GetByPath(ctx, accountID, parentPath)
		if err != nil {
			return nil, err
		}
	}

	var newParent *folder.Folder
	newName := newPath
	if strings.Contains(newPath, folder.PathSeparator) {
		parentPath := newPath[:strings.LastIndex(newPath, folder.PathSeparator)]
		newName = newPath[len(parentPath)+1:]
		var err error
		newParent, err = f.repo.GetByPath(ctx, accountID, parentPath)
		if err != nil {
			if !errors.Is(err, folder.ErrNotFound) {
				return nil, err
			}

			if !createParent {
				return nil, storeerrors.NotExistsError{Text: "parent folder does not exist"}
			}

			// TODO: Limit recursion
			newParent, err = f.Create(ctx, accountID, parentPath, folder.RoleNone, createParent)
			if err != nil {
				return nil, err
			}
		}
	}

	renamed, err := f.repo.RenameMove(
		ctx, accountID,
		oldParent, newParent,
		oldName, newName,
	)
	if err != nil {
		return nil, storeerrors.InternalError{Reason: fmt.Errorf("renamemove: %w", err)}
	}

	for _, r := range renamed {
		log.Info("renamed folder",
			zap.String("old_path", r.OldPath),
			zap.String("new_path", r.NewPath),
			zap.Stringer("folder_id", r.ID),
		)
	}

	return renamed, nil
}

func (f Folder) Delete(ctx context.Context, accountID ulid.ULID, recursive bool, path string) ([]folder.DeletedFolder, error) {
	if !recursive {
		deleted, err := f.repo.GetByPath(ctx, accountID, path)
		if err != nil {
			return nil, err
		}
		if err := f.repo.Delete(ctx, deleted.ID); err != nil {
			return nil, err
		}
		contextlib.Logger(ctx).Info("deleted folder", zap.Stringer("id", deleted.ID), zap.String("path", deleted.Path))
		return []folder.DeletedFolder{
			{
				ID:   deleted.ID,
				Path: deleted.Path,
			},
		}, nil
	}
	return f.repo.DeleteTree(ctx, accountID, path)
}

func (f Folder) Subscribe(ctx context.Context, accountID ulid.ULID, path string) error {
	fold, err := f.repo.GetByPath(ctx, accountID, path)
	if err != nil {
		return err
	}

	fold.SetSubscribed(true)

	if err := f.repo.Update(ctx, fold); err != nil {
		return err
	}
	return nil
}

func (f Folder) Unsubscribe(ctx context.Context, accountID ulid.ULID, path string) error {
	fold, err := f.repo.GetByPath(ctx, accountID, path)
	if err != nil {
		return err
	}

	fold.SetSubscribed(false)

	if err := f.repo.Update(ctx, fold); err != nil {
		return err
	}
	return nil
}
