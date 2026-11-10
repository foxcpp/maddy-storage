package accountsql

import (
	"encoding/json"
	"fmt"

	"github.com/foxcpp/maddy-storage/internal/domain/account"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite/jetdb/model"
	"github.com/oklog/ulid/v2"
)

func asDTO(m *account.Account) (*model.Accounts, error) {
	namespaceBlob, err := json.Marshal(m.Namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal account namespace: %w", err)
	}

	return &model.Accounts{
		ID:        m.ID[:],
		Name:      m.Name,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
		Namespace: namespaceBlob,
	}, nil
}

func asModel(dto *model.Accounts) (*account.Account, error) {
	acct := &account.Account{
		Name:      dto.Name,
		CreatedAt: dto.CreatedAt,
		UpdatedAt: dto.UpdatedAt,
	}

	if len(dto.ID) != len(ulid.ULID{}) {
		return nil, fmt.Errorf("invalid ULID length: %d", len(dto.ID))
	}
	copy(acct.ID[:], dto.ID)

	if err := json.Unmarshal(dto.Namespace, &acct.Namespace); err != nil {
		return nil, fmt.Errorf("failed to unmarshal account namespace: %w", err)
	}

	return acct, nil
}
