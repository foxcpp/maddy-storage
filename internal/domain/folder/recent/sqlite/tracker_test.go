package recentsql_test

import (
	"context"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/foxcpp/maddy-storage/internal/domain/account"
	accountsql "github.com/foxcpp/maddy-storage/internal/domain/account/repository/sqlite"
	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/domain/folder/recent"
	recentsql "github.com/foxcpp/maddy-storage/internal/domain/folder/recent/sqlite"
	foldersqlite "github.com/foxcpp/maddy-storage/internal/domain/folder/repository/sqlite"
	"github.com/foxcpp/maddy-storage/internal/pkg/contextlib"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite"
	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

type fixture struct {
	t       *testing.T
	ctx     context.Context
	tracker recent.Tracker
	acct    *account.Account
	folders folder.Repo
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	ctx := contextlib.WithLogger(context.Background(), zaptest.NewLogger(t))

	db, err := sqlite.NewMemory(sqlite.Cfg{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	acct, err := account.NewAccount("test")
	require.NoError(t, err)
	require.NoError(t, accountsql.New(db).Create(ctx, acct))

	return &fixture{
		t: t, ctx: ctx, acct: acct,
		tracker: recentsql.New(db),
		folders: foldersqlite.New(db),
	}
}

func (f *fixture) mkFolder(name string) ulid.ULID {
	f.t.Helper()

	fld, err := folder.NewFolder(nil, f.acct.ID, name, folder.RoleNone)
	require.NoError(f.t, err)
	require.NoError(f.t, f.folders.Create(f.ctx, fld))

	return fld.ID
}

func uids(set recent.Set) []int {
	return set.AsIMAPUIDList()
}

func TestTracker_AddGetCount(t *testing.T) {
	f := newFixture(t)
	a, b := f.mkFolder("a"), f.mkFolder("b")

	require.NoError(t, f.tracker.AddRecent(f.ctx, a, 1, 5))
	require.NoError(t, f.tracker.AddRecent(f.ctx, a, 3, 6))
	require.NoError(t, f.tracker.AddRecent(f.ctx, b, 1, 7))

	got, err := f.tracker.GetRecents(f.ctx, a, 100)
	require.NoError(t, err)
	require.Equal(t, []int{1, 3}, uids(got))
	require.Equal(t, 2, got.Len())
	require.True(t, got.IsRecent(3))
	require.False(t, got.IsRecent(2))

	cnt, err := f.tracker.CountRecent(f.ctx, a)
	require.NoError(t, err)
	require.Equal(t, uint32(2), cnt)

	cnt, err = f.tracker.CountRecent(f.ctx, ulid.Make())
	require.NoError(t, err)
	require.Zero(t, cnt)
}

func TestTracker_GetRecentsModSeqLe(t *testing.T) {
	f := newFixture(t)
	a := f.mkFolder("a")

	require.NoError(t, f.tracker.AddRecent(f.ctx, a, 1, 5))
	require.NoError(t, f.tracker.AddRecent(f.ctx, a, 2, 6))
	require.NoError(t, f.tracker.AddRecent(f.ctx, a, 3, 7))

	got, err := f.tracker.GetRecents(f.ctx, a, 6)
	require.NoError(t, err)
	require.Equal(t, []int{1, 2}, uids(got))

	got, err = f.tracker.GetRecents(f.ctx, a, 4)
	require.NoError(t, err)
	require.True(t, got.Empty())
}

func TestTracker_GetRecentsDoesNotRemove(t *testing.T) {
	f := newFixture(t)
	a := f.mkFolder("a")

	require.NoError(t, f.tracker.AddRecent(f.ctx, a, 1, 5))

	for range 2 {
		got, err := f.tracker.GetRecents(f.ctx, a, 100)
		require.NoError(t, err)
		require.Equal(t, 1, got.Len())
	}
}

func TestTracker_PopRecents(t *testing.T) {
	f := newFixture(t)
	a, b := f.mkFolder("a"), f.mkFolder("b")

	require.NoError(t, f.tracker.AddRecent(f.ctx, a, 1, 5))
	require.NoError(t, f.tracker.AddRecent(f.ctx, a, 2, 6))
	require.NoError(t, f.tracker.AddRecent(f.ctx, a, 3, 7))
	require.NoError(t, f.tracker.AddRecent(f.ctx, b, 1, 5))

	got, err := f.tracker.PopRecents(f.ctx, a, 6)
	require.NoError(t, err)
	require.Equal(t, []int{1, 2}, uids(got))
	require.Equal(t, 2, got.Size)

	left, err := f.tracker.GetRecents(f.ctx, a, 100)
	require.NoError(t, err)
	require.Equal(t, []int{3}, uids(left), "entries above modseq must stay")

	other, err := f.tracker.GetRecents(f.ctx, b, 100)
	require.NoError(t, err)
	require.Equal(t, []int{1}, uids(other), "other folders must not be affected")

	again, err := f.tracker.PopRecents(f.ctx, a, 6)
	require.NoError(t, err)
	require.True(t, again.Empty(), "second pop has nothing to return")
}

func TestTracker_AddRecentIsIdempotent(t *testing.T) {
	f := newFixture(t)
	a := f.mkFolder("a")

	require.NoError(t, f.tracker.AddRecent(f.ctx, a, 1, 5))
	require.NoError(t, f.tracker.AddRecent(f.ctx, a, 1, 9), "duplicate must be ignored")

	got, err := f.tracker.GetRecents(f.ctx, a, 6)
	require.NoError(t, err)
	require.Equal(t, []int{1}, uids(got), "the first modseq is kept")
}

func TestTracker_AddRecentEntries(t *testing.T) {
	f := newFixture(t)
	a, b := f.mkFolder("a"), f.mkFolder("b")

	ents := []folder.Entry{
		folder.NewEntry(a, ulid.Make(), 1, 5, time.Now()),
		folder.NewEntry(a, ulid.Make(), 2, 6, time.Now()),
		folder.NewEntry(b, ulid.Make(), 1, 7, time.Now()),
	}
	require.NoError(t, f.tracker.AddRecentEntries(f.ctx, ents))
	require.NoError(t, f.tracker.AddRecentEntries(f.ctx, ents), "duplicates must be ignored")

	gotA, err := f.tracker.GetRecents(f.ctx, a, 100)
	require.NoError(t, err)
	require.Equal(t, []int{1, 2}, uids(gotA))

	gotB, err := f.tracker.GetRecents(f.ctx, b, 6)
	require.NoError(t, err)
	require.True(t, gotB.Empty(), "modseq of entry must be stored")
}

func TestTracker_AddRecentEntriesEmpty(t *testing.T) {
	f := newFixture(t)

	require.NoError(t, f.tracker.AddRecentEntries(f.ctx, nil))
}

func TestTracker_AddRecentUnknownFolder(t *testing.T) {
	f := newFixture(t)

	err := f.tracker.AddRecent(f.ctx, ulid.Make(), imap.UID(1), 1)
	require.Error(t, err, "recent_uids references folders")
}
