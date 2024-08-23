package usecase

import (
	"context"
	"errors"
	"time"

	"github.com/foxcpp/maddy-storage/internal/domain/account"
	"github.com/foxcpp/maddy-storage/internal/domain/changelog"
	"github.com/oklog/ulid/v2"
)

var ErrInvalidCredentials = errors.New("invalid credentials")

type Auth interface {
	Login(ctx context.Context, username, password string) (string, error)
}

type StubAuth struct{}

func (s StubAuth) Login(_ context.Context, username, _ string) (string, error) {
	return username, nil
}

type Account struct {
	repo      account.Repo
	auth      Auth
	changeLog changelog.Repo
}

func NewAccount(repo account.Repo, auth Auth, changeLog changelog.Repo) Account {
	return Account{
		repo:      repo,
		auth:      auth,
		changeLog: changeLog,
	}
}

func (a Account) GetByName(ctx context.Context, name string) (*account.Account, error) {
	return a.repo.GetByName(ctx, name)
}

func (a Account) ListAll(ctx context.Context) ([]account.Account, error) {
	return a.repo.GetAll(ctx, time.Time{}, account.OrderID)
}

func (a Account) Create(ctx context.Context, name string) (*account.Account, error) {
	acct, err := account.NewAccount(name)
	if err != nil {
		return nil, err
	}

	if err := a.repo.Create(ctx, acct); err != nil {
		return nil, err
	}

	return acct, nil
}

func (a Account) DeleteByName(ctx context.Context, name string) (ulid.ULID, error) {
	acct, err := a.repo.GetByName(ctx, name)
	if err != nil {
		return ulid.ULID{}, err
	}

	err = a.repo.Delete(ctx, acct.ID_)
	if err != nil {
		return ulid.ULID{}, err
	}

	return acct.ID_, nil
}

func (a Account) AuthPlain(ctx context.Context, username, password string) (ulid.ULID, error) {
	authzUsername, err := a.auth.Login(ctx, username, password)
	if err != nil {
		return ulid.ULID{}, err
	}

	acct, err := a.repo.GetByName(ctx, authzUsername)
	if err != nil {
		if errors.Is(err, account.ErrNotFound) {
			return ulid.ULID{}, ErrInvalidCredentials
		}
		return ulid.ULID{}, err
	}

	return acct.ID_, nil
}
