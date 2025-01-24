package account

import (
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/oklog/ulid/v2"
)

type Account struct {
	ID        ulid.ULID
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
	Namespace folder.Namespace // Root folder namespace configuration.
}

func NewAccount(name string) (*Account, error) {
	if !utf8.ValidString(name) {
		return nil, fmt.Errorf("account name must be valid utf8")
	}

	now := time.Now()
	return &Account{
		ID:        ulid.Make(),
		Name:      name,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}
