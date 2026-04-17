package recent

import (
	"context"
	"strings"

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

func (set *Set) Contains(uid uint32) bool {
	return set.Set.Contains(imap.UID(uid))
}

func (set *Set) AsIMAPUIDList() []int {
	res := make([]int, 0, set.Size)
	for _, r := range set.Set {
		for i := r.Start; i <= r.Stop; i++ {
			res = append(res, int(i))
		}
	}

	return res
}

type Tracker interface {
	GetRecents(ctx context.Context, folderID ulid.ULID, modSeqLe folder.ModSeq) (Set, error)
	PopRecents(ctx context.Context, folderID ulid.ULID, modSeqLe folder.ModSeq) (Set, error)
	AddRecent(ctx context.Context, folderID ulid.ULID, uid imap.UID, modSeq folder.ModSeq) error
	AddRecentEntries(ctx context.Context, ents []folder.Entry) error
	CountRecent(ctx context.Context, folderID ulid.ULID) (uint32, error)
}

type ctxKey struct{}

var ctxKeyValue ctxKey

func WithSet(ctx context.Context, set Set) context.Context {
	return context.WithValue(ctx, ctxKeyValue, set)
}

func SetFromContext(ctx context.Context) (Set, bool) {
	v := ctx.Value(ctxKeyValue)
	if v == nil {
		return Set{}, false
	}
	set, ok := v.(Set)
	return set, ok
}

const FlagString = `\Recent`

func FilterRecentFlag(flags []string) (hasRecent bool, otherFlags []string) {
	for _, f := range flags {
		if strings.EqualFold(f, FlagString) {
			hasRecent = true
		} else {
			otherFlags = append(otherFlags, f)
		}
	}
	return hasRecent, otherFlags
}
