package recentsql

import (
	"context"
	"fmt"

	"github.com/emersion/go-imap/v2"
	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/domain/folder/recent"
	"github.com/foxcpp/maddy-storage/internal/pkg/storeerrors"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlcommon"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite/jetdb/model"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite/jetdb/table"
	j "github.com/go-jet/jet/v2/sqlite"
	"github.com/oklog/ulid/v2"
)

type Tracker struct {
	// TODO: Limit recents set size.
	db sqlcommon.DB
}

func New(db sqlcommon.DB) Tracker {
	return Tracker{db: db}
}

func blob(id ulid.ULID) j.BlobExpression {
	return j.Blob(id[:])
}

func asDTO(folderID ulid.ULID, uid uint32, modSeq folder.ModSeq) model.RecentUids {
	return model.RecentUids{
		FolderID: folderID[:],
		UID:      int64(uid),
		Modseq:   int64(modSeq),
	}
}

func asSet(rows []model.RecentUids) recent.Set {
	set := imap.UIDSet{}
	for _, r := range rows {
		set.AddNum(imap.UID(r.UID))
	}
	return recent.Set{Set: set, Size: len(rows)}
}

func where(folderID ulid.ULID, modSeqLe folder.ModSeq) j.BoolExpression {
	return table.RecentUids.FolderID.EQ(blob(folderID)).
		AND(table.RecentUids.Modseq.LT_EQ(j.Int(int64(modSeqLe))))
}

func (t Tracker) PopRecents(ctx context.Context, folderID ulid.ULID, modSeqLe folder.ModSeq) (recent.Set, error) {
	var rows []model.RecentUids
	stmt := table.RecentUids.DELETE().
		WHERE(where(folderID, modSeqLe)).
		RETURNING(table.RecentUids.UID)
	if err := t.db.Query(ctx, stmt, &rows); err != nil {
		return recent.Set{}, storeerrors.InternalError{Reason: fmt.Errorf("failed to fetch recent flag state: %w", err)}
	}
	return asSet(rows), nil
}

func (t Tracker) GetRecents(ctx context.Context, folderID ulid.ULID, modSeqLe folder.ModSeq) (recent.Set, error) {
	var rows []model.RecentUids
	stmt := j.SELECT(table.RecentUids.AllColumns).
		FROM(table.RecentUids).
		WHERE(where(folderID, modSeqLe))
	if err := t.db.Query(ctx, stmt, &rows); err != nil {
		return recent.Set{}, storeerrors.InternalError{Reason: fmt.Errorf("failed to fetch recent flag state: %w", err)}
	}
	return asSet(rows), nil
}

func (t Tracker) insert(ctx context.Context, rows []model.RecentUids) error {
	if len(rows) == 0 {
		return nil
	}

	stmt := table.RecentUids.INSERT(table.RecentUids.AllColumns).
		MODELS(rows).
		ON_CONFLICT().
		DO_NOTHING()
	if _, err := t.db.Exec(ctx, stmt); err != nil {
		return storeerrors.InternalError{Reason: fmt.Errorf("failed to save recent flag state: %w", err)}
	}
	return nil
}

func (t Tracker) AddRecent(ctx context.Context, folderID ulid.ULID, uid imap.UID, modSeq folder.ModSeq) error {
	return t.insert(ctx, []model.RecentUids{asDTO(folderID, uint32(uid), modSeq)})
}

func (t Tracker) AddRecentEntries(ctx context.Context, ents []folder.Entry) error {
	rows := make([]model.RecentUids, len(ents))
	for i, ent := range ents {
		rows[i] = asDTO(ent.FolderID, ent.IMAPUID, ent.ModSeq)
	}
	return t.insert(ctx, rows)
}

func (t Tracker) CountRecent(ctx context.Context, folderID ulid.ULID) (uint32, error) {
	var dest struct{ Count int64 }
	stmt := j.SELECT(j.COUNT(j.STAR).AS("count")).
		FROM(table.RecentUids).
		WHERE(table.RecentUids.FolderID.EQ(blob(folderID)))
	if err := t.db.Query(ctx, stmt, &dest); err != nil {
		return 0, storeerrors.InternalError{Reason: fmt.Errorf("failed to fetch recent flag state: %w", err)}
	}

	return uint32(dest.Count), nil
}
