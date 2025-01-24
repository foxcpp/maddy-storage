package folder

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/foxcpp/maddy-storage/internal/domain/metadata"
	"github.com/oklog/ulid/v2"
)

const PathSeparator = "/"
const FolderINBOX = "INBOX"

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
	ID        ulid.ULID
	ParentID  ulid.ULID // can be empty if at root
	AccountID ulid.ULID

	Name string // mutable via repository only
	Path string // mutable via repository only

	Role       Role // mutable
	Subscribed bool // mutable
	SortOrder  uint // mutable

	Metadata_ metadata.Md // mutable
	CreatedAt time.Time
	UpdatedAt time.Time

	// UpdatedAt when the model is restored to prevent
	// race conditions in read-modify-save.
	InitialUpdatedAt time.Time
}

func (f *Folder) IsAncestorOf(other *Folder) bool {
	return strings.HasPrefix(other.Path, f.Path) && other.Path != f.Path
}

func (f *Folder) HasParent() bool {
	return f.ParentID != ulid.ULID{}
}

func (f *Folder) SetSubscribed(sub bool) {
	f.Subscribed = sub
	f.UpdatedAt = time.Now()
}

func (f *Folder) SetRole(flag Role) {
	f.Role = flag
	f.UpdatedAt = time.Now()
}

func (f *Folder) SetSortOrder(i uint) {
	f.SortOrder = i
	f.UpdatedAt = time.Now()
}

func NewFolder(parent *Folder, accountID ulid.ULID, name string, role Role) (*Folder, error) {
	if strings.Contains(name, PathSeparator) {
		return nil, fmt.Errorf("name cannot contain path separator")
	}
	if !utf8.ValidString(name) {
		return nil, fmt.Errorf("name must be valid utf-8")
	}

	if strings.EqualFold(name, FolderINBOX) {
		name = FolderINBOX
	}

	now := time.Now()
	folder := &Folder{
		ID:               ulid.Make(),
		ParentID:         ulid.ULID{},
		AccountID:        accountID,
		Name:             name,
		Role:             role,
		Subscribed:       false,
		SortOrder:        0,
		Metadata_:        metadata.New(),
		CreatedAt:        now,
		UpdatedAt:        now,
		InitialUpdatedAt: now,
	}

	if parent != nil {
		if parent.AccountID != accountID {
			return nil, fmt.Errorf(
				"parent (account ID = %v) must belong to the same account (%v) as created folder",
				parent.AccountID, accountID,
			)
		}
		folder.Path = parent.Path + PathSeparator + folder.Name
		folder.ParentID = parent.ID
	} else {
		folder.Path = folder.Name
	}

	return folder, nil
}
