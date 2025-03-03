package recent

import (
	"context"

	"github.com/emersion/go-imap/v2"
	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/oklog/ulid/v2"
)

type Set struct {
	Set  imap.UIDSet
	Size int
}

func (set *Set) IsRecent(uid imap.UID) bool {
	return set.Set.Contains(uid)
}

func (set *Set) Len() int {
	return set.Size
}

func (set *Set) MergeWith(other *Set) {
	if set == other {
		return
	}

	set.Set.AddSet(other.Set)
	// Because recent sets are non-interacting we can just add sizes.
	set.Size += other.Size
}

func (set *Set) Empty() bool {
	return set.Size == 0
}

type Tracker interface {
	GetRecents(ctx context.Context, folderID ulid.ULID, modSeqLe folder.ModSeq) (Set, error)
	PopRecents(ctx context.Context, folderID ulid.ULID, modSeqLe folder.ModSeq) (Set, error)
	AddRecent(ctx context.Context, folderID ulid.ULID, uid imap.UID, modSeq folder.ModSeq) error
	AddRecentEntries(ctx context.Context, ents []folder.Entry) error
	CountRecent(ctx context.Context, folderID ulid.ULID) (uint32, error)
}
