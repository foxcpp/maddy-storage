package messagesqlite

import (
	"context"
	"fmt"
	"runtime/trace"
	"time"

	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/domain/message"
	"github.com/foxcpp/maddy-storage/internal/pkg/storeerrors"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlcommon"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite/jetdb/model"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite/jetdb/table"
	j "github.com/go-jet/jet/v2/sqlite"
	"github.com/oklog/ulid/v2"
)

type repo struct {
	db sqlcommon.DB
}

func New(db sqlcommon.DB) message.Repo {
	return repo{db: db}
}

func blob(id ulid.ULID) j.BlobExpression {
	return j.Blob(id[:])
}

func blobs(ids []ulid.ULID) []j.Expression {
	res := make([]j.Expression, len(ids))
	for i, id := range ids {
		res[i] = blob(id)
	}
	return res
}

func strs(ss []string) []j.Expression {
	res := make([]j.Expression, len(ss))
	for i, s := range ss {
		res[i] = j.String(s)
	}
	return res
}

func (r repo) inTx(ctx context.Context, fn func(r repo) error) error {
	return r.db.Tx(ctx, false, func(tx sqlcommon.DB) error {
		return fn(repo{db: tx})
	})
}

func (r repo) selectFlags(ctx context.Context, ids []ulid.ULID) ([]model.MessageFlags, error) {
	var dtos []model.MessageFlags

	stmt := j.SELECT(table.MessageFlags.AllColumns).
		FROM(table.MessageFlags).
		WHERE(table.MessageFlags.MessageID.IN(blobs(ids)...)).
		ORDER_BY(table.MessageFlags.Flag.ASC())
	if err := r.db.Query(ctx, stmt, &dtos); err != nil {
		return nil, err
	}

	return dtos, nil
}

// flagsOf returns all flags of the messages, message without flags is
// not present in the map.
func (r repo) flagsOf(ctx context.Context, ids []ulid.ULID) (map[ulid.ULID][]string, error) {
	res := make(map[ulid.ULID][]string, len(ids))
	if len(ids) == 0 {
		return res, nil
	}

	dtos, err := r.selectFlags(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, d := range dtos {
		id, err := ulidFromBlob(d.MessageID)
		if err != nil {
			return nil, err
		}
		res[id] = append(res[id], d.Flag)
	}

	return res, nil
}

func (r repo) GetByID(ctx context.Context, id ulid.ULID) (*message.Msg, error) {
	defer trace.StartRegion(ctx, "maddy-storage/message.repository.sqlite.GetByID").End()

	msgs, err := r.GetByIDs(ctx, 0, id)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, message.ErrNotFound
	}

	return &msgs[0], nil
}

func (r repo) GetByIDs(ctx context.Context, modSeqGt folder.ModSeq, ids ...ulid.ULID) ([]message.Msg, error) {
	defer trace.StartRegion(ctx, "maddy-storage/message.repository.sqlite.GetByIDs").End()

	if len(ids) == 0 {
		return []message.Msg{}, nil
	}

	var (
		msgs  []model.Messages
		parts []model.MessageParts
	)

	// It is important to fetch flag -> part -> msg.
	// to ensure we won't see messages with missing parts and flags
	// because they are partially being deleted.

	flags, err := r.selectFlags(ctx, ids)
	if err != nil {
		return nil, storeerrors.InternalError{Reason: fmt.Errorf("find flags: %w", err)}
	}

	partsStmt := j.SELECT(table.MessageParts.AllColumns).
		FROM(table.MessageParts).
		WHERE(table.MessageParts.MessageID.IN(blobs(ids)...)).
		ORDER_BY(table.MessageParts.Order.ASC())
	if err := r.db.Query(ctx, partsStmt, &parts); err != nil {
		return nil, storeerrors.InternalError{Reason: fmt.Errorf("find parts: %w", err)}
	}

	where := table.Messages.ID.IN(blobs(ids)...)
	if modSeqGt != 0 {
		where = where.AND(table.Messages.Modseq.GT(j.Int(int64(modSeqGt))))
	}
	msgsStmt := j.SELECT(table.Messages.AllColumns).
		FROM(table.Messages).
		WHERE(where)
	if err := r.db.Query(ctx, msgsStmt, &msgs); err != nil {
		return nil, storeerrors.InternalError{Reason: fmt.Errorf("find msgs: %w", err)}
	}

	type joined struct {
		msg   *model.Messages
		flags []model.MessageFlags
		parts []model.MessageParts
	}

	byID := make(map[ulid.ULID]*joined, len(msgs))
	for i := range msgs {
		id, err := ulidFromBlob(msgs[i].ID)
		if err != nil {
			return nil, storeerrors.InternalError{Reason: err}
		}
		byID[id] = &joined{msg: &msgs[i]}
	}
	for _, p := range parts {
		id, err := ulidFromBlob(p.MessageID)
		if err != nil {
			return nil, storeerrors.InternalError{Reason: err}
		}
		if jm, ok := byID[id]; ok {
			jm.parts = append(jm.parts, p)
		}
	}
	for _, f := range flags {
		id, err := ulidFromBlob(f.MessageID)
		if err != nil {
			return nil, storeerrors.InternalError{Reason: err}
		}
		if jm, ok := byID[id]; ok {
			jm.flags = append(jm.flags, f)
		}
	}

	// Preserve the order of ids, callers rely on it (e.g. FETCH responses
	// are expected in sequence number order). Map iteration order is random.
	models := make([]message.Msg, 0, len(ids))
	for _, id := range ids {
		jm, ok := byID[id]
		if !ok {
			continue
		}
		// Guard against duplicate ids in the input.
		delete(byID, id)

		m, err := asModel(jm.msg, jm.flags, jm.parts)
		if err != nil {
			return nil, storeerrors.InternalError{Reason: fmt.Errorf("restore msg %v: %w", id, err)}
		}
		models = append(models, *m)
	}

	return models, nil
}

func (r repo) Create(ctx context.Context, msgs ...message.Msg) error {
	defer trace.StartRegion(ctx, "maddy-storage/message.repository.sqlite.Create").End()

	return r.inTx(ctx, func(tx repo) error {
		for i := range msgs {
			msg, flags, parts, err := asDTO(&msgs[i])
			if err != nil {
				return storeerrors.InternalError{Reason: fmt.Errorf("as dto: %w", err)}
			}

			stmt := table.Messages.INSERT(table.Messages.AllColumns).MODEL(msg)
			if _, err := tx.db.Exec(ctx, stmt); err != nil {
				// TODO: Foreign key constraints, etc.
				return storeerrors.InternalError{Reason: fmt.Errorf("create msg: %w", err)}
			}

			if _, err := tx.addFlags(ctx, flags); err != nil {
				return storeerrors.InternalError{Reason: fmt.Errorf("create flags: %w", err)}
			}

			if len(parts) == 0 {
				continue
			}
			stmt = table.MessageParts.INSERT(table.MessageParts.AllColumns).MODELS(parts)
			if _, err := tx.db.Exec(ctx, stmt); err != nil {
				// TODO: Foreign key constraints, etc.
				return storeerrors.InternalError{Reason: fmt.Errorf("create parts: %w", err)}
			}
		}
		return nil
	})
}

// addFlags inserts flags, flags that already exist are ignored. Returns
// whether any flag was really added.
func (r repo) addFlags(ctx context.Context, flags []model.MessageFlags) (bool, error) {
	if len(flags) == 0 {
		return false, nil
	}

	stmt := table.MessageFlags.INSERT(table.MessageFlags.AllColumns).
		MODELS(flags).
		ON_CONFLICT(table.MessageFlags.MessageID, table.MessageFlags.Flag).
		DO_NOTHING()
	res, err := r.db.Exec(ctx, stmt)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}

	return n > 0, nil
}

func (r repo) DeleteByID(ctx context.Context, ids ...ulid.ULID) error {
	defer trace.StartRegion(ctx, "maddy-storage/message.repository.sqlite.DeleteByID").End()

	if len(ids) == 0 {
		return nil
	}

	stmt := table.Messages.DELETE().WHERE(table.Messages.ID.IN(blobs(ids)...))
	if _, err := r.db.Exec(ctx, stmt); err != nil {
		// TODO: Foreign key constraints, etc.
		return storeerrors.InternalError{Reason: fmt.Errorf("delete msg: %w", err)}
	}

	return nil
}

// lockMsgs sets newModSeq for messages having modseq <= modSeqLe and returns
// their previous modseqs. Messages that did not pass the check are not present
// in the result.
func (r repo) lockMsgs(ctx context.Context, ids []ulid.ULID, modSeqLe, newModSeq folder.ModSeq) (map[ulid.ULID]folder.ModSeq, error) {
	prev := make(map[ulid.ULID]folder.ModSeq, len(ids))
	if len(ids) == 0 {
		return prev, nil
	}

	var dtos []model.Messages
	sel := j.SELECT(table.Messages.ID, table.Messages.Modseq).
		FROM(table.Messages).
		WHERE(table.Messages.ID.IN(blobs(ids)...).
			AND(table.Messages.Modseq.LT_EQ(j.Int(int64(modSeqLe)))))
	if err := r.db.Query(ctx, sel, &dtos); err != nil {
		return nil, storeerrors.InternalError{Reason: err}
	}
	prevAll := make(map[ulid.ULID]folder.ModSeq, len(dtos))
	for _, d := range dtos {
		id, err := ulidFromBlob(d.ID)
		if err != nil {
			return nil, storeerrors.InternalError{Reason: err}
		}
		prevAll[id] = folder.ModSeq(d.Modseq)
	}

	dtos = nil
	upd := table.Messages.UPDATE().
		SET(
			table.Messages.Modseq.SET(j.Int(int64(newModSeq))),
			table.Messages.UpdatedAt.SET(sqlite.Timestamp(time.Now())),
		).
		WHERE(table.Messages.ID.IN(blobs(ids)...).
			AND(table.Messages.Modseq.LT_EQ(j.Int(int64(modSeqLe))))).
		RETURNING(table.Messages.ID)
	if err := r.db.Query(ctx, upd, &dtos); err != nil {
		return nil, storeerrors.InternalError{Reason: fmt.Errorf("update modseq: %w", err)}
	}
	for _, d := range dtos {
		id, err := ulidFromBlob(d.ID)
		if err != nil {
			return nil, storeerrors.InternalError{Reason: err}
		}
		prev[id] = prevAll[id]
	}

	return prev, nil
}

func (r repo) AddFlags(ctx context.Context, ids []ulid.ULID, flags []string, modSeqLe, newModSeq folder.ModSeq) (map[ulid.ULID]message.MsgFlags, error) {
	defer trace.StartRegion(ctx, "maddy-storage/message.repository.sqlite.AddFlags").End()

	changed := make(map[ulid.ULID]message.MsgFlags, len(ids))
	if len(ids) == 0 || len(flags) == 0 {
		return changed, nil
	}

	err := r.inTx(ctx, func(tx repo) error {
		locked, err := tx.lockMsgs(ctx, ids, modSeqLe, newModSeq)
		if err != nil {
			return err
		}

		var changedIDs []ulid.ULID
		for id := range locked {
			added, err := tx.addFlags(ctx, flagsAsDTO(id, flags))
			if err != nil {
				return storeerrors.InternalError{Reason: fmt.Errorf("create flags: %w", err)}
			}
			if added {
				changedIDs = append(changedIDs, id)
			}
		}

		allFlags, err := tx.flagsOf(ctx, changedIDs)
		if err != nil {
			return storeerrors.InternalError{Reason: fmt.Errorf("select flags: %w", err)}
		}
		for _, id := range changedIDs {
			changed[id] = message.MsgFlags{
				ID:             id,
				Flags:          allFlags[id],
				ModSeq:         newModSeq,
				PreviousModSeq: locked[id],
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return changed, nil
}

func (r repo) ReplaceFlags(ctx context.Context, ids []ulid.ULID, flags []string, modSeqLe, newModSeq folder.ModSeq) (map[ulid.ULID]message.MsgFlags, error) {
	defer trace.StartRegion(ctx, "maddy-storage/message.repository.sqlite.ReplaceFlags").End()

	changed := make(map[ulid.ULID]message.MsgFlags, len(ids))
	if len(ids) == 0 {
		return changed, nil
	}

	err := r.inTx(ctx, func(tx repo) error {
		locked, err := tx.lockMsgs(ctx, ids, modSeqLe, newModSeq)
		if err != nil {
			return err
		}
		if len(locked) == 0 {
			return nil
		}

		lockedIDs := make([]ulid.ULID, 0, len(locked))
		for id := range locked {
			lockedIDs = append(lockedIDs, id)
		}

		stmt := table.MessageFlags.DELETE().
			WHERE(table.MessageFlags.MessageID.IN(blobs(lockedIDs)...))
		if _, err := tx.db.Exec(ctx, stmt); err != nil {
			return storeerrors.InternalError{Reason: fmt.Errorf("delete flags: %w", err)}
		}

		for _, id := range lockedIDs {
			if _, err := tx.addFlags(ctx, flagsAsDTO(id, flags)); err != nil {
				return storeerrors.InternalError{Reason: fmt.Errorf("create flags: %w", err)}
			}

			changed[id] = message.MsgFlags{
				ID:             id,
				Flags:          flags,
				ModSeq:         newModSeq,
				PreviousModSeq: locked[id],
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return changed, nil
}

func (r repo) DeleteFlags(ctx context.Context, ids []ulid.ULID, flags []string, modSeqLe, newModSeq folder.ModSeq) (map[ulid.ULID]message.MsgFlags, error) {
	defer trace.StartRegion(ctx, "maddy-storage/message.repository.sqlite.DeleteFlags").End()

	changed := make(map[ulid.ULID]message.MsgFlags, len(ids))
	if len(ids) == 0 || len(flags) == 0 {
		return changed, nil
	}

	err := r.inTx(ctx, func(tx repo) error {
		locked, err := tx.lockMsgs(ctx, ids, modSeqLe, newModSeq)
		if err != nil {
			return err
		}
		if len(locked) == 0 {
			return nil
		}

		lockedIDs := make([]ulid.ULID, 0, len(locked))
		for id := range locked {
			lockedIDs = append(lockedIDs, id)
		}

		var deleted []model.MessageFlags
		stmt := table.MessageFlags.DELETE().
			WHERE(table.MessageFlags.MessageID.IN(blobs(lockedIDs)...).
				AND(table.MessageFlags.Flag.IN(strs(flags)...))).
			RETURNING(table.MessageFlags.MessageID)
		if err := tx.db.Query(ctx, stmt, &deleted); err != nil {
			return storeerrors.InternalError{Reason: fmt.Errorf("delete flags: %w", err)}
		}

		seen := make(map[ulid.ULID]struct{}, len(deleted))
		var changedIDs []ulid.ULID
		for _, d := range deleted {
			id, err := ulidFromBlob(d.MessageID)
			if err != nil {
				return storeerrors.InternalError{Reason: err}
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			changedIDs = append(changedIDs, id)
		}

		allFlags, err := tx.flagsOf(ctx, changedIDs)
		if err != nil {
			return storeerrors.InternalError{Reason: fmt.Errorf("select flags: %w", err)}
		}
		for _, id := range changedIDs {
			changed[id] = message.MsgFlags{
				ID:             id,
				Flags:          allFlags[id],
				ModSeq:         newModSeq,
				PreviousModSeq: locked[id],
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return changed, nil
}

func (r repo) TouchMsgs(ctx context.Context, ids []ulid.ULID, newModSeq folder.ModSeq) error {
	defer trace.StartRegion(ctx, "maddy-storage/message.repository.sqlite.TouchMsgs").End()

	if len(ids) == 0 {
		return nil
	}

	stmt := table.Messages.UPDATE().
		SET(
			table.Messages.Modseq.SET(j.IntExp(j.Func("max", table.Messages.Modseq, j.Int(int64(newModSeq))))),
			table.Messages.UpdatedAt.SET(sqlite.Timestamp(time.Now())),
		).
		WHERE(table.Messages.ID.IN(blobs(ids)...))
	if _, err := r.db.Exec(ctx, stmt); err != nil {
		return storeerrors.InternalError{Reason: fmt.Errorf("update modseq: %w", err)}
	}

	return nil
}

func (r repo) DeletableExternalIDs(ctx context.Context, ids []string) ([]string, error) {
	defer trace.StartRegion(ctx, "maddy-storage/message.repository.sqlite.DeletableExternalIDs").End()

	if len(ids) == 0 {
		return nil, nil
	}

	var dtos []model.MessagesExternalIDCounters
	stmt := j.SELECT(table.MessagesExternalIDCounters.ExternalID).
		FROM(table.MessagesExternalIDCounters).
		WHERE(table.MessagesExternalIDCounters.ExternalID.IN(strs(ids)...).
			AND(table.MessagesExternalIDCounters.Copies.EQ(j.Int(0))))
	if err := r.db.Query(ctx, stmt, &dtos); err != nil {
		return nil, storeerrors.InternalError{Reason: err}
	}

	deletable := make([]string, len(dtos))
	for i, d := range dtos {
		deletable[i] = d.ExternalID
	}
	return deletable, nil
}

// DanglingMsgIDs returns messages not referenced by any folder entry.
//
// XXX: Racy with message creation if message and its entries are not stored
// atomically.
func (r repo) DanglingMsgIDs(ctx context.Context, limit int) ([]ulid.ULID, error) {
	defer trace.StartRegion(ctx, "maddy-storage/message.repository.sqlite.DanglingMsgIDs").End()

	var dtos []model.Messages
	stmt := j.SELECT(table.Messages.ID).
		FROM(table.Messages).
		WHERE(j.NOT(j.EXISTS(
			j.SELECT(j.Int(1)).
				FROM(table.FolderEntries).
				WHERE(table.FolderEntries.MessageID.EQ(table.Messages.ID)),
		))).
		ORDER_BY(table.Messages.ID.ASC()).
		LIMIT(int64(limit))
	if err := r.db.Query(ctx, stmt, &dtos); err != nil {
		return nil, storeerrors.InternalError{Reason: err}
	}

	ids := make([]ulid.ULID, len(dtos))
	for i, d := range dtos {
		id, err := ulidFromBlob(d.ID)
		if err != nil {
			return nil, storeerrors.InternalError{Reason: err}
		}
		ids[i] = id
	}
	return ids, nil
}
