package folder

import (
	"cmp"
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

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

type Range struct {
	Values    []uint32
	Intervals []NumInterval
	SeqNum    bool

	At        ModSeq
	DeletesAt ModSeq
}

func (r Range) Empty() bool {
	return len(r.Values) == 0 && len(r.Intervals) == 0
}

func (r Range) Includes(num uint32) bool {
	for _, id := range r.Values {
		if id == num {
			return true
		}
	}
	for _, id := range r.Intervals {
		if num >= id.Since && num <= id.Until {
			return true
		}
	}

	return false
}

type NumInterval struct {
	Since uint32 // inclusive, != 0
	Until uint32 // inclusive, != 0
}

func (r NumInterval) String() string {
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
		return lhs.SortOrder < rhs.SortOrder
	case OrderBySortOrderDesc:
		return lhs.SortOrder >= rhs.SortOrder
	case OrderByCreatedAt:
		return lhs.CreatedAt.Before(rhs.CreatedAt)
	case OrderByCreatedAtDesc:
		return !lhs.CreatedAt.Before(rhs.CreatedAt)
	case OrderByName:
		return strings.Compare(lhs.Name, rhs.Name) == -1
	case OrderByNameDesc:
		return strings.Compare(lhs.Name, rhs.Name) != -1
	default:
		panic("unknown sort order")
	}
}

func (o Order) Compare(lhs, rhs *Folder) int {
	switch o {
	case OrderBySortOrder:
		return cmp.Compare(lhs.SortOrder, rhs.SortOrder)
	case OrderBySortOrderDesc:
		return cmp.Compare(lhs.SortOrder, rhs.SortOrder)
	case OrderByCreatedAt:
		return lhs.CreatedAt.Compare(rhs.CreatedAt)
	case OrderByCreatedAtDesc:
		return rhs.CreatedAt.Compare(lhs.CreatedAt)
	case OrderByName:
		return strings.Compare(lhs.Name, rhs.Name)
	case OrderByNameDesc:
		return strings.Compare(lhs.Name, rhs.Name)
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

	CountEntryByRange(ctx context.Context, folderID ulid.ULID, ranges Range) (int, error)
	GetEntryByRange(ctx context.Context, folderID ulid.ULID, ranges Range, returnSeq bool) ([]Entry, error)
	GetEntryByIDs(ctx context.Context, folderID ulid.ULID, msgIDs []ulid.ULID) ([]Entry, error)
	CreateEntry(ctx context.Context, entry ...Entry) error
	ReplaceEntries(ctx context.Context, old []Entry, new []Entry, modSeq ModSeq) error
	DeleteEntryByIDs(ctx context.Context, folderID ulid.ULID, ids []ulid.ULID, modSeq ModSeq) ([]Entry, error)
	DeleteEntryByRange(ctx context.Context, folderID ulid.ULID, ranges Range, returnSeq bool) ([]Entry, error)
	TouchEntries(ctx context.Context, folderID ulid.ULID, ids []ulid.ULID, modSeq ModSeq) (map[ulid.ULID]ModSeq, error)

	DeletedEntries(ctx context.Context, folderID ulid.ULID, deletedLt time.Time, limit int) ([]Entry, error)

	UsedFlags(ctx context.Context, folderID ulid.ULID) ([]string, error)

	Tx(ctx context.Context, readOnly bool, f func(r Repo) error) error
}
