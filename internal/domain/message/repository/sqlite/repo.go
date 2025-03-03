package messagesqlite

import (
	"context"
	"errors"
	"fmt"
	"runtime/trace"
	"time"

	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/domain/message"
	"github.com/foxcpp/maddy-storage/internal/pkg/storeerrors"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite"
	"github.com/oklog/ulid/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type repo struct {
	db sqlite.DB
}

func New(db sqlite.DB) message.Repo {
	return repo{db: db}
}

func (r repo) fetch(tx *gorm.DB, modSeqGt folder.ModSeq, id ulid.ULID) (*msgDTO, []msgFlagDTO, []msgPartDTO, error) {
	var (
		msg   msgDTO
		flags []msgFlagDTO
		parts []msgPartDTO
	)

	// It is important to fetch flag -> part -> msg.
	// to ensure we won't see messages with missing parts and flags
	// because they are partially being deleted.

	err := tx.Model(&msgFlagDTO{}).
		Where("message_flags.message_id = ?", id).
		Order("message_flags.flag").
		Find(&flags).Error
	if err != nil {
		return nil, nil, nil, storeerrors.InternalError{Reason: fmt.Errorf("find flags: %v", err)}
	}

	err = tx.Model(&msgPartDTO{}).
		Where("message_parts.message_id = ?", id).
		Order("message_parts.order_").
		Find(&parts).Error
	if err != nil {
		return nil, nil, nil, storeerrors.InternalError{Reason: fmt.Errorf("find parts: %v", err)}
	}

	q := tx.Model(&msgDTO{}).
		Where("messages.id = ?", id)
	if modSeqGt != 0 {
		q = q.Where("message.modseq > ?", modSeqGt)
	}
	err = q.Limit(1).
		Find(&msg).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, nil, folder.ErrNotFound
		}
		return nil, nil, nil, storeerrors.InternalError{Reason: fmt.Errorf("find msg: %v", err)}
	}

	return &msg, flags, parts, nil
}

func (r repo) GetByID(ctx context.Context, id ulid.ULID) (*message.Msg, error) {
	defer trace.StartRegion(ctx, "maddy-storage/message.repository.sqlite.GetByID").End()

	var (
		msg   *msgDTO
		flags []msgFlagDTO
		parts []msgPartDTO
	)

	msg, flags, parts, err := r.fetch(r.db.Gorm(ctx), 0, id)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}

	model, err := asModel(msg, flags, parts)
	if err != nil {
		return nil, fmt.Errorf("restore msg %v: %v", msg.ID, err)
	}
	return model, nil
}

type joinedMsgDTO struct {
	msg   *msgDTO
	flags []msgFlagDTO
	parts []msgPartDTO
}

func (r repo) GetByIDs(ctx context.Context, modSeqGt folder.ModSeq, ids ...ulid.ULID) ([]message.Msg, error) {
	defer trace.StartRegion(ctx, "maddy-storage/message.repository.sqlite.GetByIDs").End()

	var (
		msgs  []msgDTO
		flags []msgFlagDTO
		parts []msgPartDTO
	)

	// It is important to fetch flag -> part -> msg.
	// to ensure we won't see messages with missing parts and flags
	// because they are partially being deleted.

	db := r.db.Gorm(ctx)

	err := db.Model(&msgFlagDTO{}).
		Where("message_flags.message_id IN ?", ids).
		Order("message_flags.flag").
		Find(&flags).Error
	if err != nil {
		return nil, storeerrors.InternalError{Reason: fmt.Errorf("find flags: %v", err)}
	}

	err = db.Model(&msgPartDTO{}).
		Where("message_parts.message_id IN (?)", ids).
		Order("message_parts.order_").
		Find(&parts).Error
	if err != nil {
		return nil, storeerrors.InternalError{Reason: fmt.Errorf("find parts: %v", err)}
	}

	q := db.Model(&msgDTO{}).
		Where("messages.id IN (?)", ids)
	if modSeqGt != 0 {
		q = q.Where("message.modseq > ?", modSeqGt)
	}
	err = q.Find(&msgs).Error
	if err != nil {
		return nil, storeerrors.InternalError{Reason: fmt.Errorf("find msgs: %v", err)}
	}

	byID := make(map[ulid.ULID]*joinedMsgDTO)
	for _, m := range msgs {
		byID[m.ID] = &joinedMsgDTO{
			msg: &m,
		}
	}
	for _, part := range parts {
		joined, ok := byID[part.MessageID]
		if !ok {
			continue
		}
		joined.parts = append(joined.parts, part)
	}
	for _, flag := range flags {
		joined, ok := byID[flag.MessageID]
		if !ok {
			continue
		}
		joined.flags = append(joined.flags, flag)
	}

	models := make([]message.Msg, 0, len(ids))
	for _, joined := range byID {
		m, err := asModel(joined.msg, joined.flags, joined.parts)
		if err != nil {
			return nil, fmt.Errorf("failed to create model for msg %v: %w", joined.msg.ID, err)
		}
		models = append(models, *m)
	}
	return models, err
}

func (r repo) Create(ctx context.Context, msgs ...message.Msg) error {
	defer trace.StartRegion(ctx, "maddy-storage/message.repository.sqlite.Create").End()

	return r.db.Gorm(ctx).Transaction(func(tx *gorm.DB) error {
		for _, model := range msgs {
			msg, flags, parts, err := asDTO(&model)
			if err != nil {
				return storeerrors.InternalError{Reason: fmt.Errorf("as dto: %v", err)}
			}

			err = tx.Create(msg).Error
			if err != nil {
				// TODO: Foreign key constraints, etc.
				return storeerrors.InternalError{Reason: fmt.Errorf("create msg: %v", err)}
			}

			if len(flags) != 0 {
				err = tx.Clauses(clause.OnConflict{
					Columns:   []clause.Column{{Name: "message_id"}, {Name: "flag"}},
					DoNothing: true,
				}).Create(flags).Error
				if err != nil {
					return storeerrors.InternalError{Reason: fmt.Errorf("create flags: %v", err)}
				}
			}

			err = tx.Create(parts).Error
			if err != nil {
				// TODO: Foreign key constraints, etc.
				return storeerrors.InternalError{Reason: fmt.Errorf("create parts: %v", err)}
			}
		}
		return nil
	})
}

func (r repo) DeleteByID(ctx context.Context, ids ...ulid.ULID) error {
	defer trace.StartRegion(ctx, "maddy-storage/message.repository.sqlite.DeleteByID").End()

	return r.db.Gorm(ctx).Transaction(func(tx *gorm.DB) error {
		for _, id := range ids {
			err := tx.
				Where("messages.id = ?", id).
				Delete(&msgDTO{}).Error
			if err != nil {
				// TODO: Foreign key constraints, etc.
				return storeerrors.InternalError{Reason: fmt.Errorf("delete msg: %w", err)}
			}
		}
		return nil
	})
}

func (r repo) previousModSeq(ctx context.Context, tx *gorm.DB, ids []ulid.ULID, modSeqLe folder.ModSeq) (map[ulid.ULID]folder.ModSeq, error) {
	resMap := make(map[ulid.ULID]folder.ModSeq, len(ids))
	rows, err := tx.Raw(`
		SELECT id, modseq 
		FROM messages
		WHERE id IN (?)
		AND modseq <= ?`,
		ids, uint64(modSeqLe)).Rows()
	if err != nil {
		return nil, storeerrors.InternalError{Reason: err}
	}
	defer rows.Close()
	for rows.Next() {
		var id ulid.ULID
		var modSeq uint64
		if err := rows.Scan(&id, &modSeq); err != nil {
			return nil, storeerrors.InternalError{Reason: err}
		}
		resMap[id] = folder.ModSeq(modSeq)
	}
	return resMap, nil
}

func (r repo) AddFlags(ctx context.Context, ids []ulid.ULID, flags []string, modSeqLe, newModSeq folder.ModSeq) (map[ulid.ULID]message.MsgFlags, error) {
	defer trace.StartRegion(ctx, "maddy-storage/message.repository.sqlite.AddFlags").End()

	changed := make(map[ulid.ULID]message.MsgFlags, len(ids))
	return changed, r.db.Gorm(ctx).Transaction(func(tx *gorm.DB) error {
		previousModSeqs, err := r.previousModSeq(ctx, tx, ids, modSeqLe)
		if err != nil {
			return storeerrors.InternalError{Reason: err}
		}

		now := time.Now()
		var lockedIds []ulid.ULID
		err = tx.Raw(`UPDATE messages SET modseq = ?, updated_at = ? WHERE id IN (?) AND modseq <= ? RETURNING id`,
			newModSeq, now, ids, modSeqLe).
			Pluck("id", &lockedIds).Error
		if err != nil {
			return storeerrors.InternalError{Reason: fmt.Errorf("update modseq: %v", err)}
		}

		for _, id := range lockedIds {
			flagDTOs := make([]msgFlagDTO, len(flags))
			for i, flag := range flags {
				flagDTOs[i] = msgFlagDTO{
					MessageID: id,
					Flag:      flag,
				}
			}
			q := tx.Model(&msgFlagDTO{}).Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "message_id"}, {Name: "flag"}},
				DoNothing: true,
			}).Create(flagDTOs)
			rowsAffected, err := q.RowsAffected, q.Error
			if err != nil {
				return storeerrors.InternalError{Reason: fmt.Errorf("create flags: %v", err)}
			}
			if rowsAffected == 0 {
				continue
			}

			var allFlags []string
			err = tx.Model(&msgFlagDTO{}).
				Where("message_flags.message_id = ?", id).
				Pluck("flag", &allFlags).Error
			if err != nil {
				return storeerrors.InternalError{Reason: fmt.Errorf("pluck flags: %v", err)}
			}

			changed[id] = message.MsgFlags{
				ID:             id,
				Flags:          allFlags,
				ModSeq:         newModSeq,
				PreviousModSeq: previousModSeqs[id],
			}
		}
		return nil
	})
}

func (r repo) ReplaceFlags(ctx context.Context, ids []ulid.ULID, flags []string, modSeqLe, newModSeq folder.ModSeq) (map[ulid.ULID]message.MsgFlags, error) {
	defer trace.StartRegion(ctx, "maddy-storage/message.repository.sqlite.ReplaceFlags").End()

	changed := make(map[ulid.ULID]message.MsgFlags, len(ids))
	return changed, r.db.Gorm(ctx).Transaction(func(tx *gorm.DB) error {
		previousModSeqs, err := r.previousModSeq(ctx, tx, ids, modSeqLe)
		if err != nil {
			return storeerrors.InternalError{Reason: err}
		}

		now := time.Now()
		var lockedIds []ulid.ULID
		err = tx.Raw(`UPDATE messages SET modseq = ?, updated_at = ? WHERE id IN (?) AND modseq <= ? RETURNING id`,
			newModSeq, now, ids, modSeqLe).
			Pluck("id", &lockedIds).Error
		if err != nil {
			return storeerrors.InternalError{Reason: fmt.Errorf("update modseq: %v", err)}
		}

		err = tx.
			Where("message_flags.message_id IN (?)", lockedIds).
			Delete(&msgFlagDTO{}).Error
		if err != nil {
			return storeerrors.InternalError{Reason: fmt.Errorf("delete flags: %v", err)}
		}

		for _, id := range lockedIds {
			flagDTOs := make([]msgFlagDTO, len(flags))
			for i, flag := range flags {
				flagDTOs[i] = msgFlagDTO{
					MessageID: id,
					Flag:      flag,
				}
			}
			q := tx.Model(&msgFlagDTO{}).Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "message_id"}, {Name: "flag"}},
				DoNothing: true,
			}).Create(flagDTOs)
			rowsAffected, err := q.RowsAffected, q.Error
			if err != nil {
				return storeerrors.InternalError{Reason: fmt.Errorf("create flags: %v", err)}
			}
			if rowsAffected == 0 {
				continue
			}

			changed[id] = message.MsgFlags{
				ID:             id,
				Flags:          flags,
				ModSeq:         newModSeq,
				PreviousModSeq: previousModSeqs[id],
			}
		}
		return nil
	})
}

func (r repo) DeleteFlags(ctx context.Context, ids []ulid.ULID, flags []string, modSeqLe, newModSeq folder.ModSeq) (map[ulid.ULID]message.MsgFlags, error) {
	defer trace.StartRegion(ctx, "maddy-storage/message.repository.sqlite.DeleteFlags").End()

	changed := make(map[ulid.ULID]message.MsgFlags, len(ids))
	return changed, r.db.Gorm(ctx).Transaction(func(tx *gorm.DB) error {
		previousModSeqs, err := r.previousModSeq(ctx, tx, ids, modSeqLe)
		if err != nil {
			return storeerrors.InternalError{Reason: err}
		}

		now := time.Now()
		var lockedIds []ulid.ULID
		err = tx.Raw(`UPDATE messages SET modseq = ?, updated_at = ? WHERE id IN (?) AND modseq <= ? RETURNING id`,
			newModSeq, now, ids, modSeqLe).
			Pluck("id", &lockedIds).Error
		if err != nil {
			return storeerrors.InternalError{Reason: fmt.Errorf("update modseq: %v", err)}
		}

		// TODO: Rewrite for better batching.
		for _, id := range ids {
			flagDTOs := make([]msgFlagDTO, len(flags))
			for i, flag := range flags {
				flagDTOs[i] = msgFlagDTO{
					MessageID: id,
					Flag:      flag,
				}
			}
			q := tx.Delete(flagDTOs)
			rowsAffected, err := q.RowsAffected, q.Error
			if err != nil {
				return storeerrors.InternalError{Reason: fmt.Errorf("delete flags: %v", err)}
			}
			if rowsAffected == 0 {
				continue
			}

			var allFlags []string
			err = tx.Model(&msgFlagDTO{}).
				Where("message_flags.message_id = ?", id).
				Pluck("flag", &allFlags).Error
			if err != nil {
				return storeerrors.InternalError{Reason: fmt.Errorf("pluck flags: %v", err)}
			}

			changed[id] = message.MsgFlags{
				ID:             id,
				Flags:          allFlags,
				ModSeq:         newModSeq,
				PreviousModSeq: previousModSeqs[id],
			}
		}
		return nil
	})
}

func (r repo) TouchMsgs(ctx context.Context, ids []ulid.ULID, newModSeq folder.ModSeq) error {
	err := r.db.Gorm(ctx).Exec(`UPDATE messages SET modseq = max(modseq, ?), updated_at = ? WHERE id IN (?)`,
		newModSeq, time.Now(), ids).Error
	if err != nil {
		return storeerrors.InternalError{Reason: fmt.Errorf("update modseq: %v", err)}
	}
	return nil
}

func (r repo) DeletableExternalIDs(ctx context.Context, ids []string) ([]string, error) {
	var deletable []string
	err := r.db.Gorm(ctx).Select("external_id").
		Table("messages_external_id_counters").
		Where("external_id IN (?)", ids).
		Where("copies = 0").
		Pluck("external_id", &deletable).Error
	return deletable, err
}

func (r repo) DanglingMsgIDs(ctx context.Context, limit int) ([]ulid.ULID, error) {
	var ids []ulid.ULID
	err := r.db.Gorm(ctx).Select("id").
		Table("messages").
		Where("account_id IS NULL").
		Limit(limit).
		Pluck("id", &ids).Error
	return ids, err
}
