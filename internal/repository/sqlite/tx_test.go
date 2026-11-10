package sqlite_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/foxcpp/maddy-storage/internal/repository/sqlcommon"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite/jetdb/model"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite/jetdb/table"
	j "github.com/go-jet/jet/v2/sqlite"
	"github.com/stretchr/testify/require"
)

func insertAccount(ctx context.Context, tx sqlcommon.Tx, id byte, name string) error {
	now := time.Now()
	stmt := table.Accounts.INSERT(table.Accounts.AllColumns).MODEL(model.Accounts{
		ID:        []byte{id},
		Name:      name,
		CreatedAt: now,
		UpdatedAt: now,
		Namespace: []byte("{}"),
	})
	_, err := tx.Exec(ctx, stmt)
	return err
}

func countAccounts(ctx context.Context, t *testing.T, db sqlcommon.Tx) int {
	t.Helper()

	var dest struct{ Count int64 }
	stmt := j.SELECT(j.COUNT(j.STAR).AS("count")).FROM(table.Accounts)
	require.NoError(t, db.Query(ctx, stmt, &dest))
	return int(dest.Count)
}

func TestTxRollback(t *testing.T) {
	db, err := sqlite.NewMemory(sqlite.Cfg{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errRollback := errors.New("rollback")
	err = db.Tx(ctx, false, func(tx sqlcommon.Tx) error {
		require.NoError(t, insertAccount(ctx, tx, 1, "tx-rollback"))
		require.Equal(t, 1, countAccounts(ctx, t, tx), "tx must observe its own writes")
		return errRollback
	})
	require.ErrorIs(t, err, errRollback)

	require.Equal(t, 0, countAccounts(ctx, t, db), "rolled back insert must not be visible")
}

func TestTxCommit(t *testing.T) {
	db, err := sqlite.NewMemory(sqlite.Cfg{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = db.Tx(ctx, false, func(tx sqlcommon.Tx) error {
		return insertAccount(ctx, tx, 1, "tx-commit")
	})
	require.NoError(t, err)

	require.Equal(t, 1, countAccounts(ctx, t, db))
}
