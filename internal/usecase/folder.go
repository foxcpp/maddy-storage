package usecase

import (
	"context"
	"strings"

	"github.com/foxcpp/maddy-storage/internal/domain/changelog"
	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/oklog/ulid/v2"
)

type Folder struct {
	repo      folder.Repo
	changeLog changelog.Repo
}

func NewFolder(repo folder.Repo, changeLog changelog.Repo) Folder {
	return Folder{repo: repo, changeLog: changeLog}
}

type ListOpts struct {
	Filter           folder.Filter
	DescendantFilter *folder.Filter

	CheckChildren bool

	CountMsgs    bool
	CountDeleted bool
	CountUnseen  bool
	CountSize    bool

	SortAsTree bool
}

type FolderData struct {
	Folder             folder.Folder
	MatchingDescendant []folder.Folder

	HasChildren bool

	Msgs        int
	DeletedMsgs int
	UnseenMsgs  int
	Size        int64
}

func (f Folder) List(ctx context.Context, accountID ulid.ULID, opts *ListOpts, order folder.Order) ([]FolderData, error) {
	foundRoots, err := f.repo.GetByAccount(ctx, accountID, opts.Filter, order)
	if err != nil {
		return nil, err
	}
	if len(foundRoots) == 0 {
		return nil, nil
	}

	dataList := make([]FolderData, len(foundRoots))
	for i, fold := range foundRoots {
		data := FolderData{
			Folder: fold,
		}

		if opts.DescendantFilter != nil {
			prefix := fold.Path_ + folder.PathSeparator
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
			cnt, err := f.repo.CountByAccount(ctx, accountID, folder.Filter{
				ParentID: &fold.ID_,
			})
			if err != nil {
				return nil, err
			}
			data.HasChildren = cnt != 0
		}

		if opts.CountMsgs {
			panic("not implemented") // TODO: implement me
		}
		if opts.CountDeleted {
			panic("not implemented") // TODO: implement me
		}
		if opts.CountUnseen {
			panic("not implemented") // TODO: implement me
		}
		if opts.CountSize {
			panic("not implemented") // TODO: implement me
		}

		dataList[i] = data
	}

	if opts.SortAsTree {
		folder.SortAsTree(dataList, func(i int) ulid.ULID {
			return dataList[i].Folder.ParentID_
		}, func(i int, j int) bool {
			return order.Less(&dataList[i].Folder, &dataList[j].Folder)
		})
	}

	return dataList, nil
}

func (f Folder) Create(ctx context.Context, accountID ulid.ULID, path string, role folder.Role) (*folder.Folder, error) {
	var parent *folder.Folder
	name := path
	if strings.Contains(path, folder.PathSeparator) {
		parentPath := path[:strings.LastIndex(path, folder.PathSeparator)]
		name = path[len(parentPath):]
		var err error
		parent, err = f.repo.GetByPath(ctx, accountID, parentPath)
		if err != nil {
			return nil, err
		}
	}

	newFolder, err := folder.NewFolder(parent, accountID, name, role)
	if err != nil {
		return nil, err
	}

	if err := f.repo.Create(ctx, newFolder); err != nil {
		return nil, err
	}

	return newFolder, nil
}

func (f Folder) Rename(ctx context.Context, accountID ulid.ULID, path, newPath string) ([]folder.RenamedFolder, error) {
	return f.repo.RenameTree(ctx, accountID, path, newPath)
}

func (f Folder) Delete(ctx context.Context, accountID ulid.ULID, path string) ([]folder.DeletedFolder, error) {
	return f.repo.DeleteTree(ctx, accountID, path)
}

func (f Folder) Subscribe(ctx context.Context, accountID ulid.ULID, path string) error {
	folder, err := f.repo.GetByPath(ctx, accountID, path)
	if err != nil {
		return err
	}

	folder.SetSubscribed(true)

	if err := f.repo.Update(ctx, folder); err != nil {
		return err
	}
	return nil
}

func (f Folder) Unsubscribe(ctx context.Context, accountID ulid.ULID, path string) error {
	folder, err := f.repo.GetByPath(ctx, accountID, path)
	if err != nil {
		return err
	}

	folder.SetSubscribed(false)

	if err := f.repo.Update(ctx, folder); err != nil {
		return err
	}
	return nil
}
