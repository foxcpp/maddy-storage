package accountsql

import (
	"encoding/json"
	"time"

	"github.com/foxcpp/maddy-storage/internal/domain/account"
	"github.com/oklog/ulid/v2"
)

type accountDTO struct {
	ID        ulid.ULID `gorm:"id"`
	Name      string    `gorm:"name"`
	CreatedAt time.Time `gorm:"created_at,autoCreateTime:false"`
	UpdatedAt time.Time `gorm:"updated_at,autoUpdateTime:false"`
	Namespace []byte    `json:"namespace"`
}

func (accountDTO) TableName() string { return "accounts" }

func asDTO(model *account.Account) *accountDTO {
	namespaceBlob, err := json.Marshal(model.Namespace)
	if err != nil {
		panic("failed to marshal account namespace: " + err.Error())
	}

	return &accountDTO{
		ID:        model.ID,
		Name:      model.Name,
		CreatedAt: model.CreatedAt,
		UpdatedAt: model.UpdatedAt,
		Namespace: namespaceBlob,
	}
}

func asModel(dto *accountDTO) *account.Account {
	acct := &account.Account{
		ID:        dto.ID,
		Name:      dto.Name,
		CreatedAt: dto.CreatedAt,
		UpdatedAt: dto.UpdatedAt,
	}
	// TODO: Validate ULID

	if err := json.Unmarshal(dto.Namespace, &acct.Namespace); err != nil {
		panic("failed to unmarshal account namespace: " + err.Error())
	}

	return acct
}
