package sqlcommon

import (
	"github.com/foxcpp/maddy-storage/internal/domain/account"
	"github.com/oklog/ulid/v2"
	"time"
)

type AccountDTO struct {
	ID        ulid.ULID `gorm:"id"`
	Name      string    `gorm:"name"`
	CreatedAt time.Time `gorm:"created_at,autoCreateTime:false"`
	UpdatedAt time.Time `gorm:"updated_at,autoUpdateTime:false"`
}

func (AccountDTO) TableName() string { return "accounts" }

func AsDTO(model *account.Account) *AccountDTO {
	return &AccountDTO{
		ID:        model.ID_,
		Name:      model.Name_,
		CreatedAt: model.CreatedAt_,
		UpdatedAt: model.UpdatedAt_,
	}
}

func AsModel(dto *AccountDTO) *account.Account {
	acct := &account.Account{
		ID_:        dto.ID,
		Name_:      dto.Name,
		CreatedAt_: dto.CreatedAt,
		UpdatedAt_: dto.UpdatedAt,
	}
	// TODO: Validate ULID

	return acct
}
