package account

import (
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/oklog/ulid/v2"
)

type Account struct {
	ID_        ulid.ULID
	Name_      string
	CreatedAt_ time.Time
	UpdatedAt_ time.Time
}

func (a *Account) ID() ulid.ULID        { return a.ID_ }
func (a *Account) Name() string         { return a.Name_ }
func (a *Account) CreatedAt() time.Time { return a.CreatedAt_ }
func (a *Account) UpdatedAt() time.Time { return a.UpdatedAt_ }

func NewAccount(name string) (*Account, error) {
	if !utf8.ValidString(name) {
		return nil, fmt.Errorf("account name must be valid utf8")
	}

	now := time.Now()
	return &Account{
		ID_:        ulid.Make(),
		Name_:      name,
		CreatedAt_: now,
		UpdatedAt_: now,
	}, nil
}
