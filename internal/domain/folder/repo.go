package folder

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/foxcpp/maddy-storage/internal/pkg/storeerrors"
	"github.com/oklog/ulid/v2"
)

var (
	ErrNotFound           = storeerrors.NotExistsError{Text: "no such folder"}
	ErrAlreadyExists      = storeerrors.AlreadyExistsError{Text: "folder with such name already exists"}
	ErrHasChildren        = storeerrors.LogicError{Text: "cannot delete folder while children folders exist"}
	ErrEntryNotFound      = storeerrors.NotExistsError{Text: "no such folder entry"}
	ErrEntryAlreadyExists = storeerrors.AlreadyExistsError{Text: "folder entry already exists"}
	ErrDanglingEntry      = storeerrors.NotExistsError{Text: "folder entry refers to non-existing message or folder"}
)

type UIDRange struct {
	Since uint32
	Until uint32
}

func (r UIDRange) String() string {
	return fmt.Sprintf("%d:%d", r.Since, r.Until)
}

type Filter struct {
	PathRegex    []*regexp.Regexp // Must be POSIX-compatible
	NameContains *string
	Path         *string // exact match
	PathPrefix   *string
	ParentID     *ulid.ULID
	ParentPath   *string
	Subscribed   *bool
	HasRole      *bool
	Role         *Role
}

type Order int

const (
	OrderBySortOrder Order = iota + 1
	OrderBySortOrderDesc
	OrderByCreatedAt
	OrderByCreatedAtDesc
	OrderByName
	OrderByNameDesc
)

func (o Order) Less(lhs, rhs *Folder) bool {
	switch o {
	case OrderBySortOrder:
		return lhs.SortOrder_ < rhs.SortOrder_
	case OrderBySortOrderDesc:
		return lhs.SortOrder_ >= rhs.SortOrder_
	case OrderByCreatedAt:
		return lhs.CreatedAt_.Before(rhs.CreatedAt_)
	case OrderByCreatedAtDesc:
		return !lhs.CreatedAt_.Before(rhs.CreatedAt_)
	case OrderByName:
		return strings.Compare(lhs.Name_, rhs.Name_) == -1
	case OrderByNameDesc:
		return strings.Compare(lhs.Name_, rhs.Name_) != -1
	default:
		panic("unknown sort order")
	}
}

type RenamedFolder struct {
	ID      ulid.ULID
	OldPath string
	NewPath string
}

type DeletedFolder struct {
	ID   ulid.ULID
	Path string
}

type Repo interface {
	GetByID(ctx context.Context, id ulid.ULID) (*Folder, error)
	GetByPath(ctx context.Context, accountID ulid.ULID, path string) (*Folder, error)
	GetByAccount(ctx context.Context, accountID ulid.ULID, f Filter, order Order) ([]Folder, error)
	CountByAccount(ctx context.Context, accountID ulid.ULID, f Filter) (int, error)

	Create(ctx context.Context, folder *Folder) error
	Update(ctx context.Context, folder *Folder) error
	Delete(ctx context.Context, folderID ulid.ULID) error
	RenameMove(
		ctx context.Context, accountID ulid.ULID,
		oldParent, newParent *Folder,
		oldName, newName string,
	) ([]RenamedFolder, error)
	DeleteTree(ctx context.Context, accountID ulid.ULID, root string) ([]DeletedFolder, error)

	NextUID(ctx context.Context, folderID ulid.ULID, n int) ([]uint32, error)
	CountEntryByUIDRange(ctx context.Context, folderID ulid.ULID, ranges ...UIDRange) (int, error)
	GetEntryByUIDRange(ctx context.Context, folderID ulid.ULID, ranges ...UIDRange) ([]Entry, error)
	CreateEntry(ctx context.Context, entry ...Entry) error
	ReplaceEntries(ctx context.Context, old []Entry, new []Entry) error
	DeleteEntryByUIDRange(ctx context.Context, folderID ulid.ULID, ranges ...UIDRange) error

	Tx(ctx context.Context, readOnly bool, f func(r Repo) error) error
}
