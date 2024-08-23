package folder

import (
	"fmt"
	"math/rand"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/foxcpp/maddy-storage/internal/domain/metadata"
	"github.com/oklog/ulid/v2"
)

const PathSeparator = "/"

type Role string

const (
	RoleNone      Role = ""
	RoleArchive   Role = "Archive"
	RoleDrafts    Role = "Drafts"
	RoleImportant Role = "Important"
	RoleInbox     Role = "Inbox"
	RoleJunk      Role = "Junk"
	RoleSent      Role = "Sent"
	RoleTrash     Role = "Trash"
)

func (r Role) Valid() bool {
	_, ok := validRoles[r]
	return ok
}

var validRoles = map[Role]struct{}{
	RoleArchive:   {},
	RoleDrafts:    {},
	RoleImportant: {},
	RoleInbox:     {},
	RoleJunk:      {},
	RoleSent:      {},
	RoleTrash:     {},
}

type Folder struct {
	ID_        ulid.ULID
	ParentID_  ulid.ULID // can be empty if at root
	AccountID_ ulid.ULID

	Name_ string // mutable via repository only
	Path_ string // mutable via repository only

	Role_       Role // mutable
	Subscribed_ bool // mutable
	SortOrder_  uint // mutable

	// IMAP-specific
	UIDValidity_ uint32
	UIDNext_     uint32 // mutable via repository only

	Metadata_  metadata.Md // mutable
	CreatedAt_ time.Time
	UpdatedAt_ time.Time

	// UpdatedAt when the model is restored to prevent
	// race conditions in read-modify-save.
	InitialUpdatedAt time.Time
}

func (f *Folder) ID() ulid.ULID        { return f.ID_ }
func (f *Folder) ParentID() ulid.ULID  { return f.ParentID_ }
func (f *Folder) AccountID() ulid.ULID { return f.AccountID_ }

func (f *Folder) Name() string { return f.Name_ }
func (f *Folder) Path() string { return f.Path_ }

func (f *Folder) Role() Role       { return f.Role_ }
func (f *Folder) Subscribed() bool { return f.Subscribed_ }
func (f *Folder) SortOrder() uint  { return f.SortOrder_ }

func (f *Folder) UIDValidity() uint32 { return f.UIDValidity_ }
func (f *Folder) UIDNext() uint32     { return f.UIDNext_ }

func (f *Folder) Metadata() metadata.Md { return f.Metadata_ }
func (f *Folder) CreatedAt() time.Time  { return f.CreatedAt_ }
func (f *Folder) UpdatedAt() time.Time  { return f.UpdatedAt_ }

func (f *Folder) SetSubscribed(sub bool) {
	f.Subscribed_ = sub
	f.UpdatedAt_ = time.Now()
}

func (f *Folder) SetRole(flag Role) {
	f.Role_ = flag
	f.UpdatedAt_ = time.Now()
}

func (f *Folder) SetSortOrder(i uint) {
	f.SortOrder_ = i
	f.UpdatedAt_ = time.Now()
}

func NewFolder(parent *Folder, accountID ulid.ULID, name string, role Role) (*Folder, error) {
	if strings.Contains(name, PathSeparator) {
		return nil, fmt.Errorf("name cannot contain path separator")
	}
	if !utf8.ValidString(name) {
		return nil, fmt.Errorf("name must be valid utf-8")
	}

	now := time.Now()
	folder := &Folder{
		ID_:              ulid.Make(),
		ParentID_:        ulid.ULID{},
		AccountID_:       accountID,
		Name_:            name,
		Role_:            role,
		Subscribed_:      false,
		SortOrder_:       0,
		UIDValidity_:     uint32(rand.Int31()),
		UIDNext_:         1,
		Metadata_:        metadata.New(),
		CreatedAt_:       now,
		UpdatedAt_:       now,
		InitialUpdatedAt: now,
	}

	if parent != nil {
		if parent.AccountID() != accountID {
			return nil, fmt.Errorf(
				"parent (account ID = %v) must belong to the same account (%v) as created folder",
				parent.AccountID(), accountID,
			)
		}
		folder.Path_ = parent.Path_ + PathSeparator + folder.Name_
		folder.ParentID_ = parent.ID_
	} else {
		folder.Path_ = folder.Name_
	}

	return folder, nil
}
