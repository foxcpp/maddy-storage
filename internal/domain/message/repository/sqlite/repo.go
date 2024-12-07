package messagesqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

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

func (r repo) fetch(tx *gorm.DB, id ulid.ULID) (*msgDTO, []msgFlagDTO, []msgPartDTO, error) {
	var (
		msg   msgDTO
		flags []msgFlagDTO
		parts []msgPartDTO
	)

	err := tx.Model(&msgDTO{}).
		Where("messages.id = ?", id).
		Limit(1).
		Find(&msg).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, nil, folder.ErrNotFound
		}
		return nil, nil, nil, storeerrors.InternalError{Reason: fmt.Errorf("find msg: %v", err)}
	}

	err = tx.Model(&msgFlagDTO{}).
		Where("message_flags.message_id = ?", id).
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

	return &msg, flags, parts, nil
}

func (r repo) GetByID(ctx context.Context, id ulid.ULID) (*message.Msg, error) {
	var (
		msg   *msgDTO
		flags []msgFlagDTO
		parts []msgPartDTO
	)

	err := r.db.Gorm(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		msg, flags, parts, err = r.fetch(tx, id)
		return err
	}, &sql.TxOptions{
		Isolation: sql.LevelRepeatableRead,
		ReadOnly:  true,
	})
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}

	model, err := asModel(msg, flags, parts)
	if err != nil {
		return nil, fmt.Errorf("restore msg %v: %v", msg.ID, err)
	}
	return model, nil
}

func (r repo) GetByIDs(ctx context.Context, ids ...ulid.ULID) ([]message.Msg, error) {
	models := make([]message.Msg, 0, len(ids))

	err := r.db.Gorm(ctx).Transaction(func(tx *gorm.DB) error {
		for _, id := range ids {
			msg, flags, parts, err := r.fetch(tx, id)
			if err != nil {
				return fmt.Errorf("fetch %v: %w", id, err)
			}

			model, err := asModel(msg, flags, parts)
			if err != nil {
				return fmt.Errorf("restore msg %v: %v", msg.ID, err)
			}

			models = append(models, *model)
		}
		return nil
	}, &sql.TxOptions{
		Isolation: sql.LevelRepeatableRead,
		ReadOnly:  true,
	})

	return models, err
}

func (r repo) Create(ctx context.Context, msgs ...message.Msg) error {
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

			err = tx.Create(flags).Error
			if err != nil {
				// TODO: Foreign key constraints, etc.
				return storeerrors.InternalError{Reason: fmt.Errorf("create flags: %v", err)}
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

func (r repo) AddFlags(ctx context.Context, ids []ulid.ULID, flags []string) (map[ulid.ULID]message.MsgFlags, error) {
	changed := make(map[ulid.ULID]message.MsgFlags, len(ids))
	return changed, r.db.Gorm(ctx).Transaction(func(tx *gorm.DB) error {
		for _, id := range ids {
			flagDTOs := make([]msgFlagDTO, len(flags))
			for i, flag := range flags {
				flagDTOs[i] = msgFlagDTO{
					MessageID: id,
					Flag:      flag,
				}
			}
			err := tx.Model(&msgFlagDTO{}).Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "message_id"}, {Name: "flag"}},
				DoNothing: true,
			}).Create(flagDTOs).Error
			if err != nil {
				return storeerrors.InternalError{Reason: fmt.Errorf("create flags: %v", err)}
			}

			var allFlags []string
			err = tx.Model(&msgFlagDTO{}).
				Where("message_flags.message_id = ?", id).
				Pluck("flag", &allFlags).Error
			if err != nil {
				return storeerrors.InternalError{Reason: fmt.Errorf("pluck flags: %v", err)}
			}
			changed[id] = message.MsgFlags{
				ID:    id,
				Flags: allFlags,
			}
		}
		return nil
	})
}

func (r repo) ReplaceFlags(ctx context.Context, ids []ulid.ULID, flags []string) (map[ulid.ULID]message.MsgFlags, error) {
	changed := make(map[ulid.ULID]message.MsgFlags, len(ids))
	return changed, r.db.Gorm(ctx).Transaction(func(tx *gorm.DB) error {
		for _, id := range ids {
			err := tx.Model(&msgFlagDTO{}).
				Where("message_flags.message_id = ?", id).
				Delete(&msgFlagDTO{}).Error
			if err != nil {
				return storeerrors.InternalError{Reason: fmt.Errorf("delete flags: %v", err)}
			}

			flagDTOs := make([]msgFlagDTO, len(flags))
			for i, flag := range flags {
				flagDTOs[i] = msgFlagDTO{
					MessageID: id,
					Flag:      flag,
				}
			}
			err = tx.Model(&msgFlagDTO{}).Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "message_id"}, {Name: "flag"}},
				DoNothing: true,
			}).Create(flagDTOs).Error
			if err != nil {
				return storeerrors.InternalError{Reason: fmt.Errorf("create flags: %v", err)}
			}

			changed[id] = message.MsgFlags{
				ID:    id,
				Flags: flags,
			}
		}
		return nil
	})
}

func (r repo) DeleteFlags(ctx context.Context, ids []ulid.ULID, flags []string) (map[ulid.ULID]message.MsgFlags, error) {
	changed := make(map[ulid.ULID]message.MsgFlags, len(ids))
	return changed, r.db.Gorm(ctx).Transaction(func(tx *gorm.DB) error {
		for _, id := range ids {
			flagDTOs := make([]msgFlagDTO, len(flags))
			for i, flag := range flags {
				flagDTOs[i] = msgFlagDTO{
					MessageID: id,
					Flag:      flag,
				}
			}
			err := tx.Delete(flagDTOs).Error
			if err != nil {
				return storeerrors.InternalError{Reason: fmt.Errorf("delete flags: %v", err)}
			}

			var allFlags []string
			err = tx.Model(&msgFlagDTO{}).
				Where("message_flags.message_id = ?", id).
				Pluck("flag", &allFlags).Error
			if err != nil {
				return storeerrors.InternalError{Reason: fmt.Errorf("pluck flags: %v", err)}
			}
			changed[id] = message.MsgFlags{
				ID:    id,
				Flags: allFlags,
			}
		}
		return nil
	})
}
