package accountsqlite

import (
	"context"
	"errors"
	"runtime/trace"
	"time"

	"github.com/foxcpp/maddy-storage/internal/domain/account"
	"github.com/foxcpp/maddy-storage/internal/pkg/storeerrors"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite"
	"github.com/oklog/ulid/v2"
	"gorm.io/gorm"
)

type repo struct {
	db sqlite.DB
}

func New(db sqlite.DB) account.Repo {
	return repo{db: db}
}

func orderKey(o account.Order) string {
	switch o {
	case account.OrderID:
		return "accounts.id"
	case account.OrderName:
		return "accounts.name"
	default:
		panic("unknown order key")
	}
}

func (r repo) GetAll(ctx context.Context, createdAtGt time.Time, order account.Order) ([]account.Account, error) {
	defer trace.StartRegion(ctx, "account.Repository.GetAll").End()

	var dto []accountDTO

	err := r.db.Gorm(ctx).
		Model(&accountDTO{}).
		Where("accounts.created_at > ?", createdAtGt).
		Order(orderKey(order)).
		Find(&dto).Error
	if err != nil {
		return nil, storeerrors.InternalError{Reason: err}
	}

	models := make([]account.Account, len(dto))
	for i, d := range dto {
		models[i] = *asModel(&d)
	}

	return models, nil
}

func (r repo) GetByID(ctx context.Context, id ulid.ULID) (*account.Account, error) {
	defer trace.StartRegion(ctx, "account.Repository.GetByID").End()

	var dto accountDTO

	err := r.db.Gorm(ctx).
		Model(&accountDTO{}).
		Where("accounts.id = ?", id).
		Limit(1).
		Find(&dto).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, account.ErrNotFound
		}
		return nil, storeerrors.InternalError{Reason: err}
	}

	model := asModel(&dto)

	return model, nil
}

func (r repo) GetByName(ctx context.Context, name string) (*account.Account, error) {
	defer trace.StartRegion(ctx, "account.Repository.GetByName").End()

	var dto accountDTO

	err := r.db.Gorm(ctx).
		Model(&accountDTO{}).
		Where("accounts.name = ?", name).
		First(&dto).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, account.ErrNotFound
		}
		return nil, storeerrors.InternalError{Reason: err}
	}

	model := asModel(&dto)

	return model, nil
}

func (r repo) Create(ctx context.Context, account *account.Account) error {
	defer trace.StartRegion(ctx, "account.Repository.Create").End()

	dto := asDTO(account)

	err := r.db.Gorm(ctx).Create(dto).Error
	if err != nil {
		// TODO: Foreign key constraints, etc.
		return storeerrors.InternalError{Reason: err}
	}

	return nil
}

func (r repo) Delete(ctx context.Context, id ulid.ULID) error {
	defer trace.StartRegion(ctx, "account.Repository.Delete").End()

	err := r.db.Gorm(ctx).
		Where("accounts.id = ?", id).
		Delete(&accountDTO{}).Error
	if err != nil {
		// TODO: Foreign key constraints, etc.
		return storeerrors.InternalError{Reason: err}
	}

	return nil
}
