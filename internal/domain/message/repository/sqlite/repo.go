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
		return nil, nil, nil, storeerrors.InternalError{Reason: err}
	}

	err = tx.Model(&msgFlagDTO{}).
		Where("message_flags.message_id = ?", id).
		Find(&flags).Error
	if err != nil {
		return nil, nil, nil, storeerrors.InternalError{Reason: err}
	}

	err = tx.Model(&msgPartDTO{}).
		Where("message_parts.message_id = ?", id).
		Find(&parts).Error
	if err != nil {
		return nil, nil, nil, storeerrors.InternalError{Reason: err}
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
		return nil, err
	}

	model, err := asModel(msg, flags, parts)
	if err != nil {
		return nil, fmt.Errorf("failed to restore msg %v: %v", msg.ID, err)
	}
	return model, nil
}

func (r repo) GetByIDs(ctx context.Context, ids ...ulid.ULID) ([]message.Msg, error) {
	models := make([]message.Msg, 0, len(ids))

	err := r.db.Gorm(ctx).Transaction(func(tx *gorm.DB) error {
		for _, id := range ids {
			msg, flags, parts, err := r.fetch(tx, id)
			if err != nil {
				return err
			}

			model, err := asModel(msg, flags, parts)
			if err != nil {
				return fmt.Errorf("failed to restore msg %v: %v", msg.ID, err)
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
				return err
			}

			err = tx.Create(msg).Error
			if err != nil {
				// TODO: Foreign key constraints, etc.
				return storeerrors.InternalError{Reason: err}
			}

			err = tx.Create(flags).Error
			if err != nil {
				// TODO: Foreign key constraints, etc.
				return storeerrors.InternalError{Reason: err}
			}

			err = tx.Create(parts).Error
			if err != nil {
				// TODO: Foreign key constraints, etc.
				return storeerrors.InternalError{Reason: err}
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
				return storeerrors.InternalError{Reason: err}
			}
		}
		return nil
	})
}
