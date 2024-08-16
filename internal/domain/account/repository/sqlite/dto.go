package accountsqlite

import (
	"time"

	"github.com/foxcpp/maddy-storage/internal/domain/account"
	"github.com/oklog/ulid/v2"
)

type accountDTO struct {
	ID        ulid.ULID `gorm:"id"`
	Name      string    `gorm:"name"`
	CreatedAt time.Time `gorm:"created_at,autoCreateTime:false"`
	UpdatedAt time.Time `gorm:"updated_at,autoUpdateTime:false"`
}

func (accountDTO) TableName() string { return "accounts" }

func asDTO(model *account.Account) *accountDTO {
	return &accountDTO{
		ID:        model.ID_,
		Name:      model.Name_,
		CreatedAt: model.CreatedAt_,
		UpdatedAt: model.UpdatedAt_,
	}
}

func asModel(dto *accountDTO) *account.Account {
	acct := &account.Account{
		ID_:        dto.ID,
		Name_:      dto.Name,
		CreatedAt_: dto.CreatedAt,
		UpdatedAt_: dto.UpdatedAt,
	}
	// TODO: Validate ULID

	return acct
}
