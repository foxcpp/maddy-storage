package accountmemory

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/foxcpp/maddy-storage/internal/domain/account"
	"github.com/oklog/ulid/v2"
)

type Repository struct {
	Accounts map[ulid.ULID]*account.Account
}

func New() *Repository {
	return &Repository{
		Accounts: make(map[ulid.ULID]*account.Account),
	}
}

func (r Repository) GetAll(_ context.Context, createdAtGt time.Time, order account.Order) ([]account.Account, error) {
	var foundAccts []account.Account
	for _, acct := range r.Accounts {
		if acct.CreatedAt.After(createdAtGt) {
			foundAccts = append(foundAccts, *acct)
		}
	}

	switch order {
	case account.OrderID:
		sort.Slice(foundAccts, func(i, j int) bool {
			return foundAccts[i].ID.Compare(foundAccts[j].ID) == -1
		})
	case account.OrderName:
		sort.Slice(foundAccts, func(i, j int) bool {
			return foundAccts[i].Name < foundAccts[j].Name
		})
	default:
		panic("unknown order key")
	}

	return foundAccts, nil
}

func (r Repository) GetByID(_ context.Context, id ulid.ULID) (*account.Account, error) {
	acct, ok := r.Accounts[id]
	if !ok {
		return nil, account.ErrNotFound
	}
	return acct, nil
}

func (r Repository) GetByName(_ context.Context, name string) (*account.Account, error) {
	var foundAcct *account.Account
	for _, acct := range r.Accounts {
		if acct.Name == name {
			foundAcct = acct
			break
		}
	}
	if foundAcct == nil {
		return nil, account.ErrNotFound
	}
	return foundAcct, nil
}

func (r Repository) Create(_ context.Context, acct *account.Account) error {
	_, exists := r.Accounts[acct.ID]
	if exists {
		return fmt.Errorf("account with id %s already exists", acct.ID.String())
	}
	r.Accounts[acct.ID] = acct
	return nil
}

func (r Repository) Delete(_ context.Context, id ulid.ULID) error {
	delete(r.Accounts, id)
	return nil
}
