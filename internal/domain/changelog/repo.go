package changelog

import (
	"context"
	"time"

	"github.com/oklog/ulid/v2"
)

type Repo interface {
	LastAccountTime(ctx context.Context, accountID ulid.ULID) (time.Time, error)
	LastFolderTime(ctx context.Context, accountID ulid.ULID) (time.Time, error)
	LastMessageTime(ctx context.Context, accountID ulid.ULID) (time.Time, error)

	GetAccountChanges(ctx context.Context, accountID ulid.ULID, atGt time.Time, limit int) ([]Entry, error)
	GetFolderChanges(ctx context.Context, folderID ulid.ULID, atGt time.Time, limit int) ([]Entry, error)
	GetMessageChanges(ctx context.Context, msgID ulid.ULID, atGt time.Time, limit int) ([]Entry, error)

	Create(ctx context.Context, entries ...Entry) error
}
