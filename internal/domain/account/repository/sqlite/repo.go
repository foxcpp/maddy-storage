package accountsql

import (
	"context"
	"runtime/trace"
	"time"

	"github.com/foxcpp/maddy-storage/internal/domain/account"
	"github.com/foxcpp/maddy-storage/internal/pkg/storeerrors"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite/jetdb/model"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite/jetdb/table"
	j "github.com/go-jet/jet/v2/sqlite"
	"github.com/oklog/ulid/v2"
)

type repo struct {
	db sqlite.DB
}

func New(db sqlite.DB) account.Repo {
	return repo{db: db}
}

func orderKey(o account.Order) j.OrderByClause {
	switch o {
	case account.OrderID:
		return table.Accounts.ID.ASC()
	case account.OrderName:
		return table.Accounts.Name.ASC()
	default:
		panic("unknown order key")
	}
}

func (r repo) GetAll(ctx context.Context, createdAtGt time.Time, order account.Order) ([]account.Account, error) {
	defer trace.StartRegion(ctx, "maddy-storage/account.repository.sqlite.GetAll").End()

	var dtos []model.Accounts

	stmt := j.SELECT(table.Accounts.AllColumns).
		FROM(table.Accounts).
		WHERE(table.Accounts.CreatedAt.GT(sqlite.Timestamp(createdAtGt))).
		ORDER_BY(orderKey(order))
	if err := r.db.Query(ctx, stmt, &dtos); err != nil {
		return nil, storeerrors.InternalError{Reason: err}
	}

	models := make([]account.Account, len(dtos))
	for i, d := range dtos {
		dto, err := asModel(&d)
		if err != nil {
			return nil, storeerrors.InternalError{Reason: err}
		}
		models[i] = *dto
	}

	return models, nil
}

func (r repo) GetByID(ctx context.Context, id ulid.ULID) (*account.Account, error) {
	defer trace.StartRegion(ctx, "maddy-storage/account.repository.sqlite.GetByID").End()

	var dto model.Accounts

	stmt := j.SELECT(table.Accounts.AllColumns).
		FROM(table.Accounts).
		WHERE(table.Accounts.ID.EQ(j.Blob(id[:]))).
		LIMIT(1)
	if err := r.db.Query(ctx, stmt, &dto); err != nil {
		return nil, storeerrors.InternalError{Reason: err}
	}

	m, err := asModel(&dto)
	if err != nil {
		return nil, storeerrors.InternalError{Reason: err}
	}

	return m, nil
}

func (r repo) GetByName(ctx context.Context, name string) (*account.Account, error) {
	defer trace.StartRegion(ctx, "maddy-storage/account.repository.sqlite.GetByName").End()

	var dto model.Accounts

	stmt := j.SELECT(table.Accounts.AllColumns).
		FROM(table.Accounts).
		WHERE(table.Accounts.Name.EQ(j.String(name))).
		LIMIT(1)
	if err := r.db.Query(ctx, stmt, &dto); err != nil {
		return nil, storeerrors.InternalError{Reason: err}
	}

	m, err := asModel(&dto)
	if err != nil {
		return nil, storeerrors.InternalError{Reason: err}
	}

	return m, nil
}

func (r repo) Create(ctx context.Context, acct *account.Account) error {
	defer trace.StartRegion(ctx, "maddy-storage/account.repository.sqlite.Create").End()

	dto, err := asDTO(acct)
	if err != nil {
		return storeerrors.InternalError{Reason: err}
	}

	stmt := table.Accounts.INSERT(table.Accounts.AllColumns).MODEL(dto)
	if _, err := r.db.Exec(ctx, stmt); err != nil {
		if sqlite.IsUniqueConstraintError(err) {
			return account.ErrAlreadyExists
		}
		return storeerrors.InternalError{Reason: err}
	}

	return nil
}

func (r repo) Delete(ctx context.Context, id ulid.ULID) error {
	defer trace.StartRegion(ctx, "maddy-storage/account.repository.sqlite.Delete").End()

	stmt := table.Accounts.DELETE().WHERE(table.Accounts.ID.EQ(j.Blob(id[:])))
	if _, err := r.db.Exec(ctx, stmt); err != nil {
		return storeerrors.InternalError{Reason: err}
	}

	return nil
}
