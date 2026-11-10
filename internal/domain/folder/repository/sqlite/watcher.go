package foldersqlite

import (
	"context"
	"time"

	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlcommon"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite/jetdb/model"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite/jetdb/table"
	j "github.com/go-jet/jet/v2/sqlite"
	"github.com/oklog/ulid/v2"
)

type Watcher struct {
	pollInterval time.Duration
	db           sqlcommon.DB
}

func NewWatcher(pollInterval time.Duration, db sqlcommon.DB) *Watcher {
	return &Watcher{
		pollInterval: pollInterval,
		db:           db,
	}
}

// changedEntryRow is folder_entries plus values from joined tables.
//
// Not embedding entryRow: QRM cannot set fields of embedded unexported types.
type changedEntryRow struct {
	model.FolderEntries

	// Projected as "changed_entry_row.seq" and "changed_entry_row.msg_modseq".
	Seq       uint32
	MsgModSeq int64
}

func (w Watcher) Sync(ctx context.Context, folders []ulid.ULID, since, deletesSince folder.ModSeq, types folder.ChangeType) ([]folder.EntryChange, error) {
	if types == folder.ChangeNone || len(folders) == 0 {
		return nil, nil
	}

	e := table.FolderEntries

	// Different from seqNums, we don't have an upper limit on seqnums here
	// so we can actually detect newer messages.
	se := table.FolderEntries.AS("seqnums_entries")
	seqnums := j.SELECT(
		se.FolderID,
		se.UID,
		j.ROW_NUMBER().OVER(j.PARTITION_BY(se.FolderID).ORDER_BY(se.UID)).AS("seq"),
	).
		FROM(se).
		WHERE(se.DeletedAt.IS_NULL().OR(se.Modseq.GT(j.Int(int64(deletesSince))))).
		AsTable("seqnums")
	seqFolder := se.FolderID.From(seqnums)
	seqUID := se.UID.From(seqnums)
	seq := j.IntegerColumn("seq").From(seqnums)

	var conds []j.BoolExpression
	if types.Includes(folder.ChangeMessageDeleted) {
		conds = append(conds, e.Modseq.GT(j.Int(int64(deletesSince))).
			AND(e.DeletedAt.IS_NOT_NULL()))
	}
	if types.Includes(folder.ChangeNewMessage) {
		conds = append(conds, e.Modseq.GT(j.Int(int64(since))).
			AND(e.Modseq.EQ(table.Messages.Modseq)).
			AND(e.DeletedAt.IS_NULL()))
	}
	if types.Includes(folder.ChangeMessageUpdated) {
		conds = append(conds, e.Modseq.NOT_EQ(table.Messages.Modseq).
			AND(table.Messages.Modseq.GT(j.Int(int64(since)))).
			AND(e.DeletedAt.IS_NULL()))
	}
	anyChange := conds[0]
	for _, c := range conds[1:] {
		anyChange = anyChange.OR(c)
	}

	stmt := j.SELECT(
		e.AllColumns,
		table.Messages.Modseq.AS("changed_entry_row.msg_modseq"),
		seq.AS("changed_entry_row.seq"),
	).
		FROM(e.
			INNER_JOIN(table.Messages, table.Messages.ID.EQ(e.MessageID)).
			INNER_JOIN(seqnums, seqUID.EQ(e.UID).AND(seqFolder.EQ(e.FolderID)))).
		WHERE(e.FolderID.IN(sqlite.ULIDList(folders)...).AND(anyChange)).
		ORDER_BY(e.FolderID, e.UID).
		LIMIT(500)

	var rows []changedEntryRow
	if err := w.db.Query(ctx, stmt, &rows); err != nil {
		return nil, err
	}

	models := make([]folder.EntryChange, 0, len(rows))
	for i := range rows {
		entry, err := entryAsModel(&entryRow{FolderEntries: rows[i].FolderEntries, Seq: rows[i].Seq})
		if err != nil {
			return nil, err
		}

		change := folder.EntryChange{}
		if !entry.DeletedAt.IsZero() {
			change.Deleted = entry
		} else if uint64(rows[i].MsgModSeq) == uint64(entry.ModSeq) {
			change.New = entry
		} else {
			change.Updated = entry
		}
		change.At = folder.ModSeq(max(uint64(entry.ModSeq), uint64(rows[i].MsgModSeq)))

		models = append(models, change)
	}
	return models, nil
}

func (w Watcher) Wait(ctx context.Context, folders []ulid.ULID, since, deletesSince folder.ModSeq, types folder.ChangeType) ([]folder.EntryChange, error) {
	return folder.Poll(ctx, w, w.pollInterval, folders, since, deletesSince, types)
}
