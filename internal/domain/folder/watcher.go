package folder

import (
	"context"
	"time"

	"github.com/oklog/ulid/v2"
)

type ChangeType int

const (
	ChangeNone       ChangeType = 0
	ChangeNewMessage ChangeType = 1 << iota
	ChangeMessageDeleted
	ChangeMessageUpdated
)

const ChangeAllMessage = ChangeNewMessage | ChangeMessageUpdated | ChangeMessageDeleted

func (c ChangeType) Includes(other ChangeType) bool {
	return c&other != 0
}

type EntryChange struct {
	At      ModSeq
	New     *Entry
	Deleted *Entry
	Updated *Entry
}

// Watcher allows to efficiently fetch changed folder entries.
//
// Note that changes entries include SeqNum that are up-to-date
// at the time specified by "since" argument for both Sync and Wait.
//
// Changes are always ordered from oldest to newest.
type Watcher interface {
	Sync(ctx context.Context, folders []ulid.ULID, since, deletesSince ModSeq, types ChangeType) ([]EntryChange, error)
	Wait(ctx context.Context, folders []ulid.ULID, since, deletesSince ModSeq, types ChangeType) ([]EntryChange, error)
}

func Poll(
	ctx context.Context, w Watcher, interval time.Duration,
	folders []ulid.ULID, since, deletesSince ModSeq,
	types ChangeType,
) ([]EntryChange, error) {
	for {
		pending, err := w.Sync(ctx, folders, since, deletesSince, types)
		if err != nil {
			return nil, err
		}
		if len(pending) > 0 {
			return pending, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
			return nil, nil
		}
	}
}
