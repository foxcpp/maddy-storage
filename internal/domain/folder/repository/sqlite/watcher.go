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

	// seqnums computes sequence numbers relative to the client's snapshot at
	// `since`. The CreatedAtModseq <= since bound matches visibleAt(): without
	// it, messages appended by concurrent sessions (created_at_modseq > since)
	// inflate the ROW_NUMBER window, making seqnums for expunge/update FETCH
	// responses exceed the client's known EXISTS count ("seq too high" in imaptest run).
	// This bounded CTE is used for Deleted and Updated entries.
	// When since==0 there is no prior snapshot, so we use the unbounded window.
	se := table.FolderEntries.AS("seqnums_entries")
	var boundedWhere j.BoolExpression
	if since > 0 {
		boundedWhere = se.CreatedAtModseq.LT_EQ(j.Int(int64(since))).
			AND(se.DeletedAt.IS_NULL().OR(se.Modseq.GT(j.Int(int64(deletesSince)))))
	} else {
		boundedWhere = se.DeletedAt.IS_NULL().OR(se.Modseq.GT(j.Int(int64(deletesSince))))
	}

	seqnums := j.SELECT(
		se.FolderID,
		se.UID,
		j.ROW_NUMBER().OVER(j.PARTITION_BY(se.FolderID).ORDER_BY(se.UID)).AS("seq"),
	).
		FROM(se).
		WHERE(boundedWhere).
		AsTable("seqnums")
	seqFolder := se.FolderID.From(seqnums)
	seqUID := se.UID.From(seqnums)
	seq := j.IntegerColumn("seq").From(seqnums)

	// newSeqnums is the unbounded window used to assign seqnums to newly
	// arrived messages. New messages have created_at_modseq > since so they
	// are absent from the bounded CTE above. Their final seqnum is their
	// position in the full current view (after all concurrent additions are
	// accounted for), which is what the client will see once EXISTS is sent.
	sn := table.FolderEntries.AS("new_seqnums_entries")
	newSeqnums := j.SELECT(
		sn.FolderID,
		sn.UID,
		j.ROW_NUMBER().OVER(j.PARTITION_BY(sn.FolderID).ORDER_BY(sn.UID)).AS("seq"),
	).
		FROM(sn).
		WHERE(sn.DeletedAt.IS_NULL().OR(sn.Modseq.GT(j.Int(int64(deletesSince))))).
		AsTable("new_seqnums")
	newSeqFolder := sn.FolderID.From(newSeqnums)
	newSeqUID := sn.UID.From(newSeqnums)
	newSeq := j.IntegerColumn("seq").From(newSeqnums)

	var conds []j.BoolExpression
	if types.Includes(folder.ChangeMessageDeleted) {
		// Only report expunges for messages the client already knows about.
		// A message added and deleted after the client's snapshot is invisible;
		// sending an EXPUNGE for it would use a seqnum beyond the client's
		// known EXISTS count. When deletesSince==0 there is no prior snapshot
		// so we apply no such filter.
		delCond := e.Modseq.GT(j.Int(int64(deletesSince))).AND(e.DeletedAt.IS_NOT_NULL())
		if deletesSince > 0 {
			delCond = delCond.AND(e.CreatedAtModseq.LT_EQ(j.Int(int64(deletesSince))))
		}
		conds = append(conds, delCond)
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
		// Use bounded seqnums for deleted/updated; unbounded for new (bounded will be NULL).
		j.COALESCE(seq, newSeq).AS("changed_entry_row.seq"),
	).
		FROM(e.
			INNER_JOIN(table.Messages, table.Messages.ID.EQ(e.MessageID)).
			LEFT_JOIN(seqnums, seqUID.EQ(e.UID).AND(seqFolder.EQ(e.FolderID))).
			LEFT_JOIN(newSeqnums, newSeqUID.EQ(e.UID).AND(newSeqFolder.EQ(e.FolderID)))).
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
