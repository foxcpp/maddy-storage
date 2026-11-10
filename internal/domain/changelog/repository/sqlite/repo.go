package changelogsqlite

import (
	"context"
	"errors"
	"runtime/trace"
	"time"

	"github.com/foxcpp/maddy-storage/internal/domain/changelog"
	"github.com/foxcpp/maddy-storage/internal/pkg/storeerrors"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite/jetdb/model"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite/jetdb/table"
	"github.com/go-jet/jet/v2/qrm"
	j "github.com/go-jet/jet/v2/sqlite"
	"github.com/oklog/ulid/v2"
)

type repo struct {
	db sqlite.DB
}

func New(db sqlite.DB) changelog.Repo {
	return repo{db: db}
}

// lastTime returns the time of the newest entry whose idCol equals id or zero
// time if there are none.
func (r repo) lastTime(ctx context.Context, idCol j.ColumnBlob, id ulid.ULID) (time.Time, error) {
	var dto model.ChangelogEntries

	stmt := j.SELECT(table.ChangelogEntries.At).
		FROM(table.ChangelogEntries).
		WHERE(idCol.EQ(sqlite.ULID(id))).
		ORDER_BY(table.ChangelogEntries.At.DESC()).
		LIMIT(1)
	if err := r.db.Query(ctx, stmt, &dto); err != nil {
		if errors.Is(err, qrm.ErrNoRows) {
			return time.Time{}, nil
		}
		return time.Time{}, storeerrors.InternalError{Reason: err}
	}

	return dto.At, nil
}

// changes returns entries whose idCol equals id, oldest first. Zero atGt and
// zero limit disable the respective filter.
func (r repo) changes(ctx context.Context, idCol j.ColumnBlob, id ulid.ULID, atGt time.Time, limit int) ([]changelog.Entry, error) {
	var dtos []model.ChangelogEntries

	cond := idCol.EQ(sqlite.ULID(id))
	if !atGt.IsZero() {
		cond = cond.AND(table.ChangelogEntries.At.GT(sqlite.Timestamp(atGt)))
	}

	stmt := j.SELECT(table.ChangelogEntries.AllColumns).
		FROM(table.ChangelogEntries).
		WHERE(cond).
		ORDER_BY(table.ChangelogEntries.At.ASC())
	if limit != 0 {
		stmt = stmt.LIMIT(int64(limit))
	}
	if err := r.db.Query(ctx, stmt, &dtos); err != nil {
		return nil, storeerrors.InternalError{Reason: err}
	}

	entries := make([]changelog.Entry, len(dtos))
	for i := range dtos {
		e, err := asModel(&dtos[i])
		if err != nil {
			return nil, storeerrors.InternalError{Reason: err}
		}
		entries[i] = *e
	}

	return entries, nil
}

func (r repo) LastAccountTime(ctx context.Context, accountID ulid.ULID) (time.Time, error) {
	defer trace.StartRegion(ctx, "maddy-storage/changelog.repository.sqlite.LastAccountTime").End()

	return r.lastTime(ctx, table.ChangelogEntries.AccountID, accountID)
}

func (r repo) LastFolderTime(ctx context.Context, folderID ulid.ULID) (time.Time, error) {
	defer trace.StartRegion(ctx, "maddy-storage/changelog.repository.sqlite.LastFolderTime").End()

	return r.lastTime(ctx, table.ChangelogEntries.FolderID, folderID)
}

func (r repo) LastMessageTime(ctx context.Context, messageID ulid.ULID) (time.Time, error) {
	defer trace.StartRegion(ctx, "maddy-storage/changelog.repository.sqlite.LastMessageTime").End()

	return r.lastTime(ctx, table.ChangelogEntries.MessageID, messageID)
}

func (r repo) GetAccountChanges(ctx context.Context, accountID ulid.ULID, atGt time.Time, limit int) ([]changelog.Entry, error) {
	defer trace.StartRegion(ctx, "maddy-storage/changelog.repository.sqlite.GetAccountChanges").End()

	return r.changes(ctx, table.ChangelogEntries.AccountID, accountID, atGt, limit)
}

func (r repo) GetFolderChanges(ctx context.Context, folderID ulid.ULID, atGt time.Time, limit int) ([]changelog.Entry, error) {
	defer trace.StartRegion(ctx, "maddy-storage/changelog.repository.sqlite.GetFolderChanges").End()

	return r.changes(ctx, table.ChangelogEntries.FolderID, folderID, atGt, limit)
}

func (r repo) GetMessageChanges(ctx context.Context, msgID ulid.ULID, atGt time.Time, limit int) ([]changelog.Entry, error) {
	defer trace.StartRegion(ctx, "maddy-storage/changelog.repository.sqlite.GetMessageChanges").End()

	return r.changes(ctx, table.ChangelogEntries.MessageID, msgID, atGt, limit)
}

func (r repo) Create(ctx context.Context, entries ...changelog.Entry) error {
	defer trace.StartRegion(ctx, "maddy-storage/changelog.repository.sqlite.Create").End()

	if len(entries) == 0 {
		return nil
	}

	dtos := make([]model.ChangelogEntries, len(entries))
	for i := range entries {
		dto, err := asDTO(&entries[i])
		if err != nil {
			return storeerrors.InternalError{Reason: err}
		}
		dtos[i] = *dto
	}

	stmt := table.ChangelogEntries.INSERT(table.ChangelogEntries.AllColumns).MODELS(dtos)
	if _, err := r.db.Exec(ctx, stmt); err != nil {
		return storeerrors.InternalError{Reason: err}
	}

	return nil
}
