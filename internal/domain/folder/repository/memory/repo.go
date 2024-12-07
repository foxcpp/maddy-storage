package foldermemory

import (
	"context"
	"sort"
	"strings"

	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/oklog/ulid/v2"
)

type Repository struct {
	Folders         map[ulid.ULID]*folder.Folder
	FoldersByAcct   map[ulid.ULID]map[string]*folder.Folder
	EntriesByFolder map[ulid.ULID][]*folder.Entry
}

func New() *Repository {
	return &Repository{
		Folders:         make(map[ulid.ULID]*folder.Folder),
		FoldersByAcct:   make(map[ulid.ULID]map[string]*folder.Folder),
		EntriesByFolder: make(map[ulid.ULID][]*folder.Entry),
	}
}

func (r Repository) matches(filter *folder.Filter, f *folder.Folder) bool {
	if filter.PathRegex != nil {
		ok := false
		for _, re := range filter.PathRegex {
			if re.MatchString(f.Path_) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if filter.NameContains != nil {
		if !strings.Contains(f.Path_, *filter.NameContains) {
			return false
		}
	}
	if filter.Path != nil {
		if f.Path_ != *filter.Path {
			return false
		}
	}
	if filter.PathPrefix != nil {
		if !strings.HasPrefix(f.Path_, *filter.PathPrefix) {
			return false
		}
	}
	if filter.ParentID != nil {
		if f.ParentID_ != *filter.ParentID {
			return false
		}
	}
	if filter.ParentPath != nil {
		if f.ParentID_ == (ulid.ULID{}) {
			return false
		}
		parent := r.Folders[f.ParentID_]
		if parent == nil {
			panic("missing parent")
		}
		if parent.Path_ != *filter.ParentPath {
			return false
		}
	}
	if filter.Subscribed != nil {
		if *filter.Subscribed != f.Subscribed_ {
			return false
		}
	}
	if filter.HasRole != nil {
		if *filter.HasRole != (f.Role_ != folder.RoleNone) {
			return false
		}
	}
	if filter.Role != nil {
		if *filter.Role != f.Role_ {
			return false
		}
	}

	return true
}

func (r Repository) GetByID(_ context.Context, id ulid.ULID) (*folder.Folder, error) {
	f, ok := r.Folders[id]
	if !ok {
		return nil, folder.ErrNotFound
	}
	return f, nil
}

func (r Repository) GetByPath(_ context.Context, accountID ulid.ULID, path string) (*folder.Folder, error) {
	acctFolders, ok := r.FoldersByAcct[accountID]
	if !ok {
		return nil, folder.ErrNotFound
	}
	f, ok := acctFolders[path]
	if !ok {
		return nil, folder.ErrNotFound
	}
	return f, nil
}

func (r Repository) GetByAccount(_ context.Context, accountID ulid.ULID, filter folder.Filter, order folder.Order) ([]folder.Folder, error) {
	var found []folder.Folder
	for _, f := range r.FoldersByAcct[accountID] {
		if r.matches(&filter, f) {
			found = append(found, *f)
		}
	}

	sort.Slice(found, func(i, j int) bool {
		return order.Less(&found[i], &found[j])
	})

	return found, nil
}

func (r Repository) CountByAccount(_ context.Context, accountID ulid.ULID, filter folder.Filter) (int, error) {
	cnt := 0
	for _, f := range r.FoldersByAcct[accountID] {
		if r.matches(&filter, f) {
			cnt++
		}
	}

	return cnt, nil
}

func (r Repository) Create(_ context.Context, f *folder.Folder) error {
	_, idExists := r.Folders[f.ID_]
	if idExists {
		return folder.ErrAlreadyExists
	}

	_, pathExists := r.FoldersByAcct[f.AccountID_][f.Path_]
	if pathExists {
		return folder.ErrAlreadyExists
	}

	r.Folders[f.ID_] = f
	r.FoldersByAcct[f.AccountID_][f.Path_] = f
	return nil
}

func (r Repository) Update(_ context.Context, f *folder.Folder) error {
	r.Folders[f.ID_] = f
	r.FoldersByAcct[f.AccountID_][f.Path_] = f
	return nil
}

func (r Repository) Delete(_ context.Context, folderID ulid.ULID) error {
	f, ok := r.Folders[folderID]
	if !ok {
		return nil
	}
	delete(r.Folders, folderID)
	delete(r.FoldersByAcct[f.AccountID_], f.Path_)
	delete(r.EntriesByFolder, folderID)
	return nil
}

func (r Repository) RenameMove(_ context.Context, accountID ulid.ULID, oldParent, newParent *folder.Folder, oldName, newName string) ([]folder.RenamedFolder, error) {
	//TODO implement me
	panic("implement me")
}

func (r Repository) DeleteTree(_ context.Context, accountID ulid.ULID, root string) ([]folder.DeletedFolder, error) {
	//TODO implement me
	panic("implement me")
}

func (r Repository) NextUID(_ context.Context, folderID ulid.ULID, n int) ([]uint32, error) {
	f, ok := r.Folders[folderID]
	if !ok {
		return nil, folder.ErrNotFound
	}
	uids := make([]uint32, n)
	for uid := f.UIDNext_; uid < f.UIDNext_+uint32(n); uid++ {
		uids = append(uids, uid)
	}
	f.UIDNext_ += uint32(n)
	return uids, nil
}

func (r Repository) CountEntryByUIDRange(_ context.Context, folderID ulid.ULID, ranges ...folder.UIDRange) (int, error) {
	//TODO implement me
	panic("implement me")
}

func (r Repository) GetEntryByUIDRange(_ context.Context, folderID ulid.ULID, ranges ...folder.UIDRange) ([]folder.Entry, error) {
	//TODO implement me
	panic("implement me")
}

func (r Repository) CreateEntry(_ context.Context, entry ...folder.Entry) error {
	//TODO implement me
	panic("implement me")
}

func (r Repository) ReplaceEntries(_ context.Context, old []folder.Entry, new []folder.Entry) error {
	//TODO implement me
	panic("implement me")
}

func (r Repository) DeleteEntryByUIDRange(_ context.Context, folderID ulid.ULID, ranges ...folder.UIDRange) error {
	//TODO implement me
	panic("implement me")
}

func (r Repository) Tx(_ context.Context, readOnly bool, f func(r folder.Repo) error) error {
	return f(r)
}
